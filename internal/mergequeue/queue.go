package mergequeue

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/goorderr"
	"github.com/simplesurance/directorius/internal/logfields"
	"github.com/simplesurance/directorius/internal/orderedmap"
	"github.com/simplesurance/directorius/internal/retry"
	"github.com/simplesurance/directorius/internal/routines"
	"github.com/simplesurance/directorius/internal/set"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// defStaleTimeout is the default stale timeout.
	// A pull request is considered as stale, when it is the first element in the
	// queue and it's state has not changed for this duration.
	defStaleTimeout = 3 * time.Hour

	// operationTimeout for how long individual GitHub and CI operations
	// are retried when an error occurs
	operationTimeout = 10 * time.Minute

	postCommentTimeout = time.Minute

	// processPRTimeout is the max. duration for which the
	// [queue.processPR] method runs for a single PR. It should be
	// bigger than [operationTimeout].
	processPRTimeout = operationTimeout * 3
)

const updateBranchPollInterval = 2 * time.Second

const appName = "directorius"

// queue implements a merge-queue for github pull requests.
// A queue is created per base branch, to that PRs are merged to.
// The queue separates pull-requests internally into active and suspended ones.
// The first PR in the active queue is the next that is gonna be merged.
// For the first PR, Jenkins CI job runs are scheduled and the PR is kept up to
// date. The actual merge of the PR is expected to be done by an external
// entity (GitHub) when all conditions are satisfied.
// For the following PRs in the active queues it is either unknown if they
// fulfill the merge requirements or they fulfill them partially. Their
// status is evaluated when they become the first in the queue.
// Analyzing the state of the first PR in the queue is done by [queue.processPR].
// Suspended PRs are PRs that do not fulfill the prerequisites for being merged
// (approval missing, failed CI check, etc).
// Queued pull requests can either be in active or suspended state. Active pull
// requests are stored in an ordered map. The first pull request in the active
// queue is processed by [queue.processPR].
type queue struct {
	baseBranch BaseBranch

	// active contains prs that are
	// requirements and are being processed in order
	active *orderedmap.Map[int, *PullRequest]
	// suspended contains pull requests that aren't satisfying the
	// requirements for being merged
	suspended map[int]*PullRequest
	lock      sync.Mutex

	logger   *zap.Logger
	ghClient GithubClient
	retryer  *retry.Retryer
	ci       *CI
	metrics  *queueMetrics

	// actionPool is a go-routine pool that runs operations on active pull
	// requests. The pool only contains 1 Go-Routine, to serialize
	// operations.
	actionPool *routines.Pool
	// executing contains information about the task that is currently
	// executed in the [queue.actionPool]
	// it's cancelFunc is used to cancel running operations.
	executing atomic.Pointer[runningOperation]

	// processPRruns counts the number of times [queue.processPRruns] has
	// been executed.
	processPRruns atomic.Uint64

	staleTimeout time.Duration
	// updateBranchPollInterval specifies the minimum pause between
	// checking if a Pull Request branch has been updated with it's base
	// branch, after GitHub returned that an update has been scheduled.
	updateBranchPollInterval time.Duration

	// headLabel is a GitHub label that is applied to the first
	// pull-request in the active queue
	headLabel string

	// paused can be set to true to skip processPR operations.
	paused atomic.Bool
}

type runningOperation struct {
	pr         int
	cancelFunc context.CancelFunc
}

func newQueue(base *BaseBranch, logger *zap.Logger, ghClient GithubClient, retryer *retry.Retryer, ci *CI, headLabel string) *queue {
	q := queue{
		baseBranch:               *base,
		active:                   orderedmap.New[int, *PullRequest](orderBefore),
		suspended:                map[int]*PullRequest{},
		logger:                   logger.Named("queue").With(base.Logfields...),
		ghClient:                 ghClient,
		retryer:                  retryer,
		actionPool:               routines.NewPool(1),
		staleTimeout:             defStaleTimeout,
		updateBranchPollInterval: updateBranchPollInterval,
		headLabel:                headLabel,
		ci:                       ci,
	}

	if qm, err := newQueueMetrics(base.BranchID); err == nil {
		q.metrics = qm
	} else {
		q.logger.Warn("creating prometheus metrics failed",
			zap.Error(err),
		)
	}

	return &q
}

// Stop empties the active and suspend queues and stops all running tasks.
// The caller must ensure that nothing is added to the queue while Stop is
// being executed.
func (q *queue) Stop() {
	q.logger.Debug("terminating")

	q.lock.Lock()
	q.suspended = map[int]*PullRequest{}
	for prNumber := range q.suspended {
		_, _ = q._dequeueSuspended(prNumber)
	}

	q.active.Foreach()(func(pr *PullRequest) bool {
		q._dequeueActive(pr.Number)
		return true
	})

	q.lock.Unlock()

	if running := q.getExecuting(); running != nil {
		running.cancelFunc()
	}

	q.logger.Debug("waiting for routines to terminate")
	q.actionPool.Wait()

	q.logger.Debug("terminated")
}

func (q *queue) String() string {
	return fmt.Sprintf("queue for base branch: %s", q.baseBranch.String())
}

// IsEmpty returns true if the queue contains no active and suspended
// pull requests.
func (q *queue) IsEmpty() bool {
	q.lock.Lock()
	defer q.lock.Unlock()

	return q.active.Len() == 0 && len(q.suspended) == 0
}

func (q *queue) _activePullRequests() []*PullRequest {
	return q.active.AsSlice()
}

func (q *queue) _suspendedPullRequests() []*PullRequest {
	result := make([]*PullRequest, 0, len(q.suspended))

	for _, v := range q.suspended {
		result = append(result, v)
	}

	return result
}

func (q *queue) asSlices() (activePRs, suspendedPRs []*PullRequest) {
	q.lock.Lock()
	defer q.lock.Unlock()

	return q._activePullRequests(), q._suspendedPullRequests()
}

func (q *queue) _enqueueActive(pr *PullRequest) error {
	logger := q.logger.With(pr.LogFields...)

	pr.SetInActiveQueueSince()
	newFirstElemen, added := q.active.InsertIfNotExist(pr.Number, pr)
	if !added {
		return fmt.Errorf("pull request already exist in active queue: %w", ErrAlreadyExists)
	}

	q.metrics.ActiveQueueSizeInc()

	if !newFirstElemen {
		q.logger.Debug("pull request appended to active queue")

		return nil
	}

	logger.Debug(
		"pull request appended to active queue, first element changed, scheduling action",
	)

	q.scheduleProcessPR(context.Background(), pr, TaskTriggerCI)

	return nil
}

// SortActiveQueue sorts the PRs in the active queue by the [orderBefore]
// criteria.
//
// The first element in the active queue is static and not reordered.
// Reordering it would mean that CI jobs most likely would need to rerun from
// the beginning and all their progress was for nothing.
func (q *queue) SortActiveQueue() {
	q.lock.Lock()
	defer q.lock.Unlock()

	if q.active.Len() == 0 {
		return
	}

	firstElemNr := q.active.First().Number
	q.active.SortByKey(func(a, b int) int {
		prA := q.active.Get(a)

		// never change the position of the first PR in the queue:
		if a == firstElemNr {
			return -1
		}
		if b == firstElemNr {
			return 1
		}

		prB := q.active.Get(b)
		return orderBefore(prA, prB)
	})
}

// Enqueue appends a pull request to the active queue.
// If it is the only element in the queue, the processPR operation is run for it.
// If it already exist, ErrAlreadyExists is returned.
func (q *queue) Enqueue(pr *PullRequest) error {
	q.lock.Lock()
	defer q.lock.Unlock()

	if _, exist := q.suspended[pr.Number]; exist {
		return fmt.Errorf("pull request already exist in suspend queue: %w", ErrAlreadyExists)
	}

	return q._enqueueActive(pr)
}

func (q *queue) _dequeueSuspended(prNumber int) (*PullRequest, error) {
	pr, exist := q.suspended[prNumber]

	if !exist {
		return nil, ErrNotFound
	}

	delete(q.suspended, prNumber)
	q.metrics.SuspendQueueSizeDec()

	return pr, nil
}

func (q *queue) _dequeueActive(prNumber int) (removedPR, newFirstPr *PullRequest) {
	oldFirst := q.active.First()
	pr := q.active.Dequeue(prNumber)
	if pr != nil {
		q.metrics.ActiveQueueSizeDec()
	}

	newFirst := q.active.First()
	if oldFirst != newFirst {
		return pr, newFirst
	}

	return pr, nil
}

// Dequeue removes the pull request with the given number from the active or
// suspended list.
// If a processPR operation is currently running for it, it is canceled.
// If the pull request does not exist in the queue, ErrNotFound is returned.
func (q *queue) Dequeue(prNumber int, setPendingStatusState bool) (*PullRequest, error) {
	q.lock.Lock()

	if pr, err := q._dequeueSuspended(prNumber); err == nil {
		q.lock.Unlock()

		logger := q.logger.With(pr.LogFields...)
		logger.Debug("pull request removed from suspend queue")

		pr.SetStateUnchangedSince(time.Time{})

		return pr, nil
	} else if !errors.Is(err, ErrNotFound) {
		q.logger.DPanic("_dequeue_suspended returned unexpected error", zap.Error(err))
	}

	removed, newFirstElem := q._dequeueActive(prNumber)
	q.lock.Unlock()

	if removed == nil {
		return nil, ErrNotFound
	}

	q.cancelActionForPR(prNumber)
	removed.SetStateUnchangedSince(time.Time{})

	q.logger.Debug("pull request removed from active queue",
		removed.LogFields...)

	q.prRemoveQueueHeadLabel(context.Background(), "dequeue", removed)
	if setPendingStatusState {
		q.prCreateCommitStatus(context.Background(), removed, "", githubclt.StatusStatePending)
	}

	if newFirstElem == nil {
		return removed, nil
	}

	q.logger.Debug("removing pr changed first element, triggering action",
		zap.Int("github.pull_request_suspended", removed.Number),
		zap.Int("github.pull_request_new_first", newFirstElem.Number),
	)

	// TODO: do we really should add the head label here?
	q.prAddQueueHeadLabel(context.Background(), newFirstElem)
	q.scheduleProcessPR(context.Background(), newFirstElem, TaskNone)

	return removed, nil
}

// suspend suspends running the processPR operation for the pull request with the given number.
// If a processPR operation is currently running for it, it is canceled.
// If is not active or not queued ErrNotFound is returned.
func (q *queue) suspend(prNumber int) error {
	q.lock.Lock()
	defer q.lock.Unlock()

	pr, newFirstElem := q._dequeueActive(prNumber)
	if pr == nil {
		return fmt.Errorf("pr not in active queue: %w", ErrNotFound)
	}
	pr.SuspendCount.Add(1)

	if _, exist := q.suspended[prNumber]; exist {
		q.logger.DPanic("pr was in active and suspend queue, removed it from active queue")
	}

	q.cancelActionForPR(prNumber)
	pr.SetStateUnchangedSince(time.Time{})

	q.suspended[prNumber] = pr
	q.metrics.SuspendQueueSizeInc()

	q.logger.Debug("pr moved to suspend queue",
		pr.LogFields...,
	)

	// The same ctx than in [q.processPRruns] must not be used here because
	// it is canceled by the [q.cancelActionForPR] call in this function!
	q.prRemoveQueueHeadLabel(context.Background(), "dequeue", pr)
	q.prCreateCommitStatus(context.Background(), pr, "", githubclt.StatusStatePending)

	if newFirstElem == nil {
		return nil
	}

	q.logger.Debug(
		"moving pr to suspend queue changed first element, triggering processPR",
		zap.Int("github.pull_request_suspended", pr.Number),
		zap.Int("github.pull_request_new_first", newFirstElem.Number),
	)

	q.scheduleProcessPR(context.Background(), newFirstElem, TaskTriggerCI)

	return nil
}

// ResumeAllPRs moves all suspended PRs to the active queue.
func (q *queue) ResumeAllPRs() {
	q.lock.Lock()
	defer q.lock.Unlock()

	for prNum, pr := range q.suspended {
		logger := q.logger.With(pr.LogFields...)

		if err := q._enqueueActive(pr); err != nil {
			logger.Error("could not move PR from suspended to active queue",
				zap.Error(err),
			)

			continue
		}

		_, _ = q._dequeueSuspended(prNum)
		logger.Info("moved pr to active queue")
	}
}

// ResumePR moves a PR from the suspend queue to the active queue.
// If the pull request is neither in the active or suspend queue ErrNotFound is
// returned.
// If the pull request is the only active pull request, processPR is scheduled.
func (q *queue) ResumePR(prNumber int) error {
	q.lock.Lock()
	pr, err := q._dequeueSuspended(prNumber)
	q.lock.Unlock()

	if err != nil {
		return err
	}

	logger := q.logger.With(pr.LogFields...)

	if err := q.Enqueue(pr); err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			logger.Warn("pr was in active and suspend queue, removed it from suspend queue")
			return nil
		}

		return fmt.Errorf("enqueing previously suspended pr failed: %w", err)
	}

	return nil
}

// FirstActive returns the first pull request in the active queue.
// If the queue is empty, nil is returned.
func (q *queue) FirstActive() *PullRequest {
	q.lock.Lock()
	defer q.lock.Unlock()

	return q.active.First()
}

// isFirstActive returns true if pr is the first one in the active queue.
func (q *queue) isFirstActive(pr *PullRequest) bool {
	first := q.FirstActive()
	return first != nil && first.Number == pr.Number
}

// ScheduleProcessPR schedules running [q.processPR] in the [q.actionPool].
func (q *queue) ScheduleProcessPR(ctx context.Context, task Task) {
	first := q.FirstActive()
	if first == nil {
		q.logger.Debug("ScheduleUpdateFirstPR was called but active queue is empty")
		return
	}

	q.scheduleProcessPR(ctx, first, task)
}

func (q *queue) scheduleProcessPR(ctx context.Context, pr *PullRequest, task Task) {
	q.actionPool.Queue(func() {
		ctx, cancelFunc := context.WithCancel(ctx)
		defer cancelFunc()

		q.setExecuting(&runningOperation{pr: pr.Number, cancelFunc: cancelFunc})
		q.processPR(ctx, pr, task)
		q.setExecuting(nil)
	})

	q.logger.Debug("update scheduled", pr.LogFields...)
}

// isPRStale returns true if the [pr.GetStateUnchangedSince] timestamp is older then
// q.staleTimeout.
func (q *queue) isPRStale(pr *PullRequest) bool {
	lastStatusChange := pr.GetStateUnchangedSince()

	if lastStatusChange.IsZero() {
		// This can be caused by a race when action() is running and
		// the PR is dequeued/suspended in the meantime.
		q.logger.Debug("stateUnchangedSince timestamp of pr is zero", pr.LogFields...)
		return false
	}

	return lastStatusChange.Add(q.staleTimeout).Before(time.Now())
}

// processPR evaluates the approval, update and CI status of the pull request
// and the actions to undertake.
// The PR must be the first in the active queue otherwise nothing is done.
//
// If the base-branch contains changes that are not in the pull request branch,
// updating it, by merging the base-branch into the PR branch, is schedule via
// the GitHub API. If updating is not possible because a merge-conflict exist
// or another error happens, a comment is posted and the pr is suspended.
// If the PR branch is uptodate, it's GitHub CI and review status is
// retrieved. If it has a failed CI status and the state is for the last
// triggered build of the CI Job or the pull request is not approved, the pr is
// suspended.
// If the status is pending a successful GitHub status from directorius is
// submitted for the HEAD commit and the queue head label is added to the pull
// request.
// If the pull request was not updated, it's GitHub check status did not change
// and it is the first element in the queue longer then q.staleTimeout it is
// suspended.
func (q *queue) processPR(ctx context.Context, pr *PullRequest, task Task) {
	loggingFields := pr.LogFields
	logger := q.logger.With(loggingFields...)

	if q.IsPaused() {
		logger.Debug("skipping update", logfields.Reason("mergequeue_paused"))
		return
	}

	pr.SetStateUnchangedSinceIfZero(time.Now())

	if err := ctx.Err(); err != nil {
		logger.Debug("skipping processing pr", zap.Error(err))
		return
	}

	if !q.isFirstActive(pr) {
		logger.Debug("skipping processing pr, pull request is not first in queue")
		return
	}

	ctx, cancelFn := context.WithTimeout(ctx, processPRTimeout)
	defer cancelFn()

	defer q.incProcessPRRuns()

	actions, err := q.evalPRAction(ctx, logger, pr)
	if err != nil {
		logger.Error(
			"evaluating status of first pull request in the queue failed",
			zap.Error(err),
		)

		if errors.Is(err, context.Canceled) {
			return
		}

		if err := q.suspend(pr.Number); err != nil {
			logger.Error("suspending PR failed", zap.Error(err))
			return
		}

		logger.Info("pull request suspended", logfields.Reason("eval_pr_status_failed"))
		return
	}

	logger.Debug("evaluated pull request actions",
		zap.Stringers("actions", actions.Actions),
		logfields.Reason(actions.Reason),
		logfields.Commit(actions.HeadCommitID),
	)

	reason := actions.Reason
	for _, action := range actions.Actions {
		switch action {
		case ActionUpdateStateUnchanged:
			pr.SetStateUnchangedSinceIfNewer(time.Now())

		case ActionSuspend:
			if err := q.suspend(pr.Number); err != nil {
				logger.Error("suspending PR failed", zap.Error(err))
				continue
			}

			logger.Info("pull request suspended", logfields.Reason(reason))

		case ActionCreatePullRequestComment:
			// use a shorter timeout to prevent that this optional operation blocks for long
			ctx, cancelFunc := context.WithTimeout(ctx, postCommentTimeout)
			defer cancelFunc()
			err := q.ghClient.CreateIssueComment(
				ctx,
				q.baseBranch.RepositoryOwner,
				q.baseBranch.Repository,
				pr.Number,
				fmt.Sprintf("%s: pull request moved to suspend queue base-branch updates suspended, updating branch failed:\n```%s```",
					appName, reason),
			)
			if err != nil {
				logger.Error("posting comment to github PR failed", zap.Error(err))
			}

		case ActionAddFirstInQueueGithubLabel:
			q.prAddQueueHeadLabel(ctx, pr)

		case ActionCreateSuccessfulGithubStatus:
			q.prCreateCommitStatus(ctx, pr, actions.HeadCommitID, githubclt.StatusStateSuccess)

		case ActionWaitForMerge:
			logger.Info("waiting for github to merge the pull-request", logfields.Reason(reason))

		case ActionTriggerCIJobs:
			if task == TaskTriggerCI {
				err := q.ci.Run(ctx, pr, actions.ExpectedCIRuns...)
				if err != nil {
					logger.Error("triggering CI jobs failed",
						zap.Error(err),
					)
					return
				}
				logger.Info("ci jobs triggered", logfields.Reason(reason))
				pr.SetStateUnchangedSinceIfNewer(time.Now())
			}

		case ActionNone:
			logger.Info("evaluated status of first pull request in queue, nothing to do", logfields.Reason(reason))
		}
	}
}

func (q *queue) updatePRWithBase(ctx context.Context, pr *PullRequest) (changed bool, headCommit string, updateBranchErr error) {
	ctx, cancelFunc := context.WithTimeout(ctx, operationTimeout)
	defer cancelFunc()

	updateBranchErr = q.retryer.Run(ctx, func(ctx context.Context) error {
		result, err := q.ghClient.UpdateBranch(
			ctx,
			q.baseBranch.RepositoryOwner,
			q.baseBranch.Repository,
			pr.Number,
		)
		if err != nil {
			return err
		}

		if result == nil {
			return errors.New("BUG: updateBranch returned nil result")
		}

		if result.Scheduled {
			changed = true
			return goorderr.NewRetryableError(
				errors.New("branch update was scheduled, retrying until update was done"),
				time.Now().Add(q.updateBranchPollInterval),
			)
		}
		if !changed {
			changed = result.Changed
		}
		headCommit = result.HeadCommitID

		return nil
	}, logfields.NewWith(pr.LogFields, logfields.Operation("update_branch")))

	if updateBranchErr != nil {
		if isPRIsClosedErr(updateBranchErr) {
			return false, "", ErrPullRequestIsClosed
		}

		return false, "", updateBranchErr
	}

	return changed, headCommit, nil
}

func isPRIsClosedErr(err error) bool {
	const wantedErrStr = "pull request is closed"

	if unWrappedErr := errors.Unwrap(err); unWrappedErr != nil {
		if strings.Contains(unWrappedErr.Error(), wantedErrStr) {
			return true
		}
	}

	return strings.Contains(err.Error(), wantedErrStr)
}

// prReadyForMergeStatus runs GitHubClient.ReadyForMergeStatus() and retries if
// it failed with a retryable error.
// The method blocks until the request was successful, a non-retryable error
// happened or the context expired.
func (q *queue) prReadyForMergeStatus(ctx context.Context, pr *PullRequest) (*githubclt.ReadyForMergeStatus, error) {
	var status *githubclt.ReadyForMergeStatus

	loggingFields := pr.LogFields

	ctx, cancelFunc := context.WithTimeout(ctx, operationTimeout)
	defer cancelFunc()

	err := q.retryer.Run(ctx, func(ctx context.Context) error {
		var err error

		status, err = q.ghClient.ReadyForMerge(
			ctx,
			q.baseBranch.RepositoryOwner,
			q.baseBranch.Repository,
			pr.Number,
		)
		if err != nil {
			return err
		}

		q.logger.Debug(
			"retrieved ready for merge status",
			append([]zap.Field{
				logfields.Commit(status.Commit),
				logfields.ReviewDecision(string(status.ReviewDecision)),
				logfields.CIStatusSummary(string(status.CIStatus)),
				zap.Any("github.ci_statuses", status.Statuses),
			}, loggingFields...)...,
		)

		return nil
	}, loggingFields)

	return status, err
}

func (q *queue) prsByBranch(branchNames set.Set[string]) (
	prs []*PullRequest, notFound set.Set[string],
) {
	q.lock.Lock()
	defer q.lock.Unlock()

	suspendedPrs, missing := q._suspendedPRsbyBranch(branchNames)
	activePrs, notFound := q._activePRsByBranch(missing)

	return append(suspendedPrs, activePrs...), notFound
}

func (q *queue) prAddQueueHeadLabel(ctx context.Context, pr *PullRequest) {
	ctx, cancelFunc := context.WithTimeout(ctx, operationTimeout)
	defer cancelFunc()
	err := q.retryer.Run(ctx, func(ctx context.Context) error {
		// if the PR already has the label, it succeeds
		return q.ghClient.AddLabel(
			ctx,
			q.baseBranch.RepositoryOwner,
			q.baseBranch.Repository,
			pr.Number,
			q.headLabel,
		)
	}, pr.LogFields)
	if err != nil {
		q.logger.Warn("adding label to PR failed",
			logfields.NewWith(pr.LogFields,
				logfields.Operation("github.add_label_failed"),
				zap.Error(err),
				zap.String("github_label", q.headLabel),
			)...)
	}

	q.logger.Info("queue head label was added to pr",
		logfields.NewWith(pr.LogFields, zap.String("github_label", q.headLabel))...)
}

func (q *queue) prRemoveQueueHeadLabel(ctx context.Context, logReason string, pr *PullRequest) {
	ctx, cancelFunc := context.WithTimeout(ctx, operationTimeout)
	defer cancelFunc()
	err := q.retryer.Run(ctx, func(ctx context.Context) error {
		return q.ghClient.RemoveLabel(ctx,
			q.baseBranch.RepositoryOwner, q.baseBranch.Repository,
			pr.Number,
			q.headLabel,
		)
	}, logfields.NewWith(pr.LogFields, logfields.Operation("github.remove_label")))
	if err != nil {
		q.logger.Warn("removing label from PR failed",
			append([]zapcore.Field{
				zap.Error(err),
				zap.String("github_label", q.headLabel),
				logfields.Reason(logReason),
			}, pr.LogFields...)...)
	}
	q.logger.Info("queue head label was removed from pr",
		append([]zapcore.Field{
			zap.String("github_label", q.headLabel),
		}, pr.LogFields...)...)
}

func (q *queue) prCreateCommitStatus(
	ctx context.Context, pr *PullRequest, commit string, state githubclt.StatusState,
) {
	err := createCommitStatus(ctx,
		q.ghClient, q.logger, q.retryer,
		q.baseBranch.RepositoryOwner, q.baseBranch.Repository,
		pr, commit,
		state,
	)
	if err != nil {
		q.logger.Error("creating github commit status failed",
			logfields.NewWith(pr.LogFields, zap.Error(err))...)
	}
}

func createCommitStatus(
	ctx context.Context,
	clt GithubClient,
	logger *zap.Logger,
	retryer *retry.Retryer,
	repositoryOwner, repository string,
	pr *PullRequest,
	commit string,
	state githubclt.StatusState,
) error {
	var desc string

	/* It is not sufficient to only set the state to pending when we
	previously set the state to successful, because the successful state
	might have been set by another PR that contains the same commit. To
	account for it we would need to store per commit which status has been
	submitted in the past. It is questionable if it is worth the additional
	complexity. Therefore we always set the pending status state.
	*/

	lf := logfields.NewWith(pr.LogFields,
		zap.String("github.status.state", string(state)),
		zap.String("github.status.description", desc),
		zap.String("github.status.context", appName),
	)
	if commit != "" {
		lf = append(lf, logfields.Commit(commit))
	}

	pr.GithubStatusLock.Lock()
	defer pr.GithubStatusLock.Unlock()

	if state == githubclt.StatusStateSuccess {
		desc = "#1 in queue"
	}

	if pr.GithubStatusLastSetState.Commit == commit && pr.GithubStatusLastSetState.State == state {
		logger.Debug("skipping setting github status state, state has already been reported for the commit")
		return nil
	}

	ctx, cancelFunc := context.WithTimeout(ctx, operationTimeout)
	defer cancelFunc()

	err := retryer.Run(ctx, func(ctx context.Context) error {
		if commit == "" {
			return clt.CreateHeadCommitStatus(ctx,
				repositoryOwner, repository, pr.Number,
				state, desc, appName,
			)
		}
		return clt.CreateCommitStatus(ctx,
			repositoryOwner, repository, commit,
			state, desc, appName,
		)
	}, logfields.NewWith(lf, logfields.Operation("github.create_commit_status")))
	if err != nil {
		return err
	}

	logger.Info("created github commit status", lf...)

	pr.GithubStatusLastSetState.Commit = commit
	pr.GithubStatusLastSetState.State = state

	return nil
}

// ActivePRsByBranch returns all pull requests that are in active state and for
// one of the branches in branchNames.
func (q *queue) ActivePRsByBranch(branchNames []string) []*PullRequest {
	branchSet := set.From(branchNames)

	q.lock.Lock()
	defer q.lock.Unlock()

	prs, _ := q._activePRsByBranch(branchSet)
	return prs
}

func (q *queue) _activePRsByBranch(branches set.Set[string]) (
	prs []*PullRequest, notFound set.Set[string],
) {
	var result []*PullRequest
	notFound = maps.Clone(branches)

	q.active.Foreach()(func(pr *PullRequest) bool {
		if branches.Contains(pr.Branch) {
			result = append(result, pr)
			delete(notFound, pr.Branch)
		}

		return true
	})

	return result, notFound
}

// SuspendedPRsbyBranch returns all pull requests that are in suspended state
// and for one of the branches in branchNames.
func (q *queue) SuspendedPRsbyBranch(branchNames []string) []*PullRequest {
	branchSet := set.From(branchNames)

	q.lock.Lock()
	defer q.lock.Unlock()

	prs, _ := q._suspendedPRsbyBranch(branchSet)
	return prs
}

func (q *queue) _suspendedPRsbyBranch(branches set.Set[string]) (
	prs []*PullRequest, notfound set.Set[string],
) {
	var result []*PullRequest
	notFound := maps.Clone(branches)

	for _, pr := range q.suspended {
		if branches.Contains(pr.Branch) {
			result = append(result, pr)
			delete(notFound, pr.Branch)
		}
	}

	return result, notFound
}

func (q *queue) resumeIfPRMergeStatusPositive(ctx context.Context, logger *zap.Logger, pr *PullRequest) error {
	if _, exist := q.suspended[pr.Number]; !exist {
		return ErrNotFound
	}

	status, err := q.prReadyForMergeStatus(ctx, pr)
	if err != nil {
		return fmt.Errorf("retrieving ready for merge status failed: %w", err)
	}

	if status.ReviewDecision != githubclt.ReviewDecisionApproved {
		logger.Info("updates for pr is not resumed, reviewdecision is not positive")
		return nil
	}

	switch status.CIStatus {
	case githubclt.CIStatusSuccess, githubclt.CIStatusPending:
		if err := q.ResumePR(pr.Number); err != nil {
			return fmt.Errorf("resuming updates failed: %w", err)
		}

		logger.Info("updates resumed, pr is approved and status check rollup is positive")

		return nil

	default:
		logger.Info("updates for prs are not resumed, status check rollup state is unsuccessful")
		return nil
	}
}

// ScheduleProcessPRIfStatusPositive schedules processPR runs for a pull
// request if they are approved, their CI status not negative.
func (q *queue) ScheduleProcessPRIfStatusPositive(ctx context.Context, pr *PullRequest) {
	q.actionPool.Queue(func() {
		logger := q.logger.With(pr.LogFields...)

		ctx, cancelFunc := context.WithCancel(ctx)
		defer cancelFunc()

		ctx, cancelFunc = context.WithTimeout(ctx, operationTimeout)
		defer cancelFunc()

		q.setExecuting(&runningOperation{pr: pr.Number, cancelFunc: cancelFunc})

		err := q.resumeIfPRMergeStatusPositive(ctx, logger, pr)
		if err != nil && !errors.Is(err, ErrNotFound) {
			q.logger.With(pr.LogFields...).Info(
				"resuming updates if pr merge status positive failed",
				zap.Error(err),
			)
		}
		q.setExecuting(nil)
	})

	q.logger.Debug("checking PR status scheduled", pr.LogFields...)
}

// SetPRStaleSinceIfNewerByBranch sets the timestamp to when the last change on
// the PR happened to t, if t is newer then the current value, for the passed
// branches.
// The function returns a Set of branch names for that no PR in the queue could
// be found.
func (q *queue) SetPRStaleSinceIfNewerByBranch(branchNames []string, t time.Time) (
	notFound set.Set[string],
) {
	branchSet := set.From(branchNames)
	prs, notFound := q.prsByBranch(branchSet)

	for _, pr := range prs {
		pr.SetStateUnchangedSinceIfNewer(t)
	}

	return notFound
}

// SetPRStaleSinceIfNewer if a PullRequest with the given number exist
// in the active queue or dequeued list, it's unchangedSince timestamp is set
// to t, if it is newer.
// If it is older, nothing is done.
// If a PR with the given number can not be found, ErrNotFound is returned.
func (q *queue) SetPRStaleSinceIfNewer(prNumber int, t time.Time) error {
	pr := q.getPullRequest(prNumber)
	if pr == nil {
		return ErrNotFound
	}

	pr.SetStateUnchangedSinceIfNewer(t)
	return nil
}

// getPullRequest returns the PullRequest with the given PrNumber if it exist in
// the suspended list or active queue.
// If it does not, nil is returned.
func (q *queue) getPullRequest(prNumber int) *PullRequest {
	q.lock.Lock()
	defer q.lock.Unlock()

	pr, exist := q.suspended[prNumber]
	if exist {
		return pr
	}

	return q.active.Get(prNumber)
}

func (q *queue) SetPullRequestPriority(prNumber int, priority int32) error {
	pr := q.getPullRequest(prNumber)
	if pr == nil {
		return ErrNotFound
	}

	pr.Priority.Store(priority)
	q.logger.Info("set pull request priority",
		logfields.NewWith(pr.LogFields, zap.Int32("priority", priority))...,
	)

	return nil
}

// Pause suspends the merge-queue.
// Checking the status and triggering CI jobs for the first PR in the queue are
// aborted and will be skipped.
func (q *queue) Pause() {
	q.logger.Info("pausing mergequeue")
	q.paused.Store(true)

	if running := q.getExecuting(); running != nil {
		running.cancelFunc()
	}
}

func (q *queue) IsPaused() bool {
	return q.paused.Load()
}

// Resume resumes the merge-queue.
// The UnchangedSince status for all PRs in the active queue is reset, the
// status of the first PR in the queue is checked and CI job runs are
// eventually triggered.
func (q *queue) Resume(ctx context.Context) {
	q.logger.Info("resuming mergequeue ")

	q.lock.Lock()
	q.active.Foreach()(func(pr *PullRequest) bool {
		pr.SetStateUnchangedSince(time.Time{})
		return true
	})
	q.lock.Unlock()

	q.paused.Store(false)
	q.ScheduleProcessPR(ctx, TaskTriggerCI)
}

// orderBefore returns:
//
//	-1 if x should be processed before y
//	 0 if x and y have the same order
//	+1 if x should be processed after y.
//
// x should be processed before y when the first of the following conditions apply:
//   - [PullRequest.Priority] is bigger.
//   - [PullRequest.InActiveQueueSince] is younger than 4h and
//     [PullRequest.SuspendCount] is smaller. (PRs with constant CI failures
//     are deprioritized. The 4h limit prevents that they starve in the queue
//     because of a flaky or temporary CI failures when there is always another
//     PR in the queue.)
//   - The [PullRequest.InActiveQueueSince] difference is >1 minute and the
//     timestamp is older. (The difference must be >1min to prevent that when
//     multiple PRs are added to the active queue again, that the one that was
//     handled first randomly is prioritzed.)
//   - The [PullRequest.EnqueuedAt] timestamp is older.
//   - The [PullRequest.Number] timestamp is smaller.
func orderBefore(x, y *PullRequest) int {
	if r := cmp.Compare(y.Priority.Load(), x.Priority.Load()); r != 0 {
		return r
	}

	xInActiveSince := x.InActiveQueueSince()
	yInActiveSince := y.InActiveQueueSince()

	if !xInActiveSince.IsZero() && !yInActiveSince.IsZero() {
		switch {
		case time.Since(xInActiveSince) < 4*time.Hour && time.Since(yInActiveSince) < 4*time.Hour:
			if r := cmp.Compare(x.SuspendCount.Load(), y.SuspendCount.Load()); r != 0 {
				return r
			}

		case time.Since(xInActiveSince) < 4*time.Hour && x.SuspendCount.Load() > 1:
			return 1

		case time.Since(yInActiveSince) < 4*time.Hour && y.SuspendCount.Load() > 1:
			return -1
		}

		if xInActiveSince.Sub(yInActiveSince).Abs() > time.Minute {
			if r := xInActiveSince.Compare(yInActiveSince); r != 0 {
				return r
			}
		}
	}

	if r := x.EnqueuedAt.Compare(y.EnqueuedAt); r != 0 {
		return r
	}

	return cmp.Compare(x.Number, y.Number)
}

func (q *queue) getExecuting() *runningOperation {
	return q.executing.Load()
}

func (q *queue) setExecuting(v *runningOperation) {
	q.executing.Store(v)
}

func (q *queue) getProcessPRRuns() uint64 {
	return q.processPRruns.Load()
}

func (q *queue) incProcessPRRuns() {
	q.processPRruns.Add(1)
}

// cancelActionForPR cancels a running update operation for the given pull
// request number.
// If none is running, nothing is done.
func (q *queue) cancelActionForPR(prNumber int) {
	if running := q.getExecuting(); running != nil {
		if running.pr == prNumber {
			running.cancelFunc()
			q.logger.Debug("cancelled running task for pr",
				logfields.PullRequest(prNumber),
			)
		}
	}
}
