package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-github/v67/github"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/logfields"
	github_prov "github.com/simplesurance/directorius/internal/provider/github"
	"github.com/simplesurance/directorius/internal/set"
)

const defPeriodicTriggerInterval = 30 * time.Minute

type GithubClient interface {
	AddLabel(ctx context.Context, owner, repo string, pullRequestOrIssueNumber int, label string) error
	CreateCommitStatus(ctx context.Context, owner, repo, commit string, state githubclt.StatusState, description, context string) error
	CreateHeadCommitStatus(ctx context.Context, owner, repo string, pullRequestNumber int, state githubclt.StatusState, description, context string) error
	CreateIssueComment(ctx context.Context, owner, repo string, issueOrPRNr int, comment string) error
	ListPRs(ctx context.Context, owner, repo string) iter.Seq2[*githubclt.PR, error]
	ReadyForMerge(ctx context.Context, owner, repo string, prNumber int) (*githubclt.ReadyForMergeStatus, error)
	RemoveLabel(ctx context.Context, owner, repo string, pullRequestOrIssueNumber int, label string) error
	UpdateBranch(ctx context.Context, owner, repo string, pullRequestNumber int) (*githubclt.UpdateBranchResult, error)
}

// Coordinator parses GitHub webhook events for Pull Requests and manages the
// merge-queues.
// It adds and removes PRs to merge queues when the [Config.TriggerOnAutomerge]
// or [Config.TriggerLabels] condition is met.
// For each git branch, that is a base branch for a PR in the merge-queue, a
// separate [queue] is created.
// Webhook events like the submission of a new Check status result in
// operations on the queue.
type Coordinator struct {
	*Config

	// periodicTriggerIntv defines the time span between triggering
	// updates for the first pull request in the queues periodically.
	// TODO: move periodicTriggerIntv to config
	periodicTriggerIntv time.Duration

	// queues contains a queue for each base-branch for which pull requests
	// are queued for being merged
	queues map[BranchID]*queue
	// queuesLock must be hold when accessing queues
	queuesLock sync.Mutex

	// wg is a waitgroup for the event loop go-routine.
	wg sync.WaitGroup
	// shutdownChan can be closed to communicate to the event loop go-routine to terminate.
	shutdownChan chan struct{}

	// processedEventCnt counts the number of events that were processed.
	// It is currently only used in testcase to delay checks until a send
	// event was processed.
	processedEventCnt atomic.Uint64
}

type PRPriorityUpdates struct {
	BranchID BranchID
	Updates  []*PRPriorityUpdate
}

type PRPriorityUpdate struct {
	PRNumber int
	Priority int32
}

func NewCoordinator(cfg Config) *Coordinator {
	a := Coordinator{
		Config:              &cfg,
		queues:              map[BranchID]*queue{},
		wg:                  sync.WaitGroup{},
		periodicTriggerIntv: defPeriodicTriggerInterval,
		shutdownChan:        make(chan struct{}, 1),
	}

	cfg.setDefaults()

	if a.DryRun {
		// TODO: let the caller of the constructor provide the dry clients instead
		a.GitHubClient = NewDryGithubClient(a.GitHubClient, a.Logger)
		a.CI.Client = NewDryCIClient(a.Logger)
		a.Logger.Info("dry run enabled")
	}

	cfg.mustValidate()

	return &a
}

// Start starts a go-routine that consumes and processes github webhook events.
func (a *Coordinator) Start() {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.eventLoop()
	}()
}

// Stop stops the event loop and waits until it terminates.
// All queues will be deleted, operations that are in progress are canceled.
func (a *Coordinator) Stop() {
	a.Logger.Debug("coordinator terminating")

	select {
	case <-a.shutdownChan: // already closed
	default:
		close(a.shutdownChan)
	}

	a.Logger.Debug("waiting for event loop to terminate")
	a.wg.Wait()

	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()

	for branchID, q := range a.queues {
		q.Stop()
		delete(a.queues, branchID)
	}

	a.Logger.Debug("coordinator terminated")
}

// isMonitoredRepository returns if a merge-queue is managed for the given
// repository.
func (a *Coordinator) isMonitoredRepository(owner, repositoryName string) bool {
	repo := Repository{
		OwnerLogin:     owner,
		RepositoryName: repositoryName,
	}

	_, exist := a.MonitoredRepositories[repo]
	return exist
}

// eventLoop receives GitHub webhook events from [Coordinator.Config.EventChan]
// and triggers processPR operations on the first element in the queues.
// Updates are triggered periodically every a.periodicTriggerIntv, to prevent
// that pull requests become stuck because GitHub webhook event was missed.
// The eventLoop terminates when a.shutdownChan is closed.
func (a *Coordinator) eventLoop() {
	a.Logger.Info("coordinator event loop started")

	periodicTrigger := time.NewTicker(a.periodicTriggerIntv)
	defer periodicTrigger.Stop()

	for {
		select {
		case event, open := <-a.EventChan:
			if !open {
				a.Logger.Info("coordinator event loop terminated")
				return
			}

			a.processEvent(context.Background(), event)

		case <-periodicTrigger.C:
			a.queuesLock.Lock()
			for _, q := range a.queues {
				q.ScheduleProcessPR(context.Background(), TaskNone)
				a.Logger.Debug("periodic run scheduled", q.baseBranch.Logfields...)
			}
			a.queuesLock.Unlock()

		case <-a.shutdownChan:
			a.Logger.Info("event loop terminating")
			return
		}
	}
}

// processEvent processes GitHub webhook events.
//
// Events for repositories that are not listed in
// [Config.MonitoredRepositories] are ignored.
// auto_merge_enabled/auto_merge_disabled events are only processed when
// [Config.TriggerOnAutomerge] is enabled.
// labeled/unlabeled events are only processed if the label is listed in
// [Config.HeadLabel]
//
// The following actions are triggered on events:
//
// Enqueue a pull request on:
//
//   - PullRequestEvent auto_merge_enabled
//   - PullRequestEvent labeled
//
// Dequeue a pull request on
//
//   - PullRequestEvent closed
//   - PullRequestEvent auto_merge_disabled
//   - PullRequestEvent unlabeled
//
// Move a pull request to another base branch queue on:
//
//   - PullRequestEvent edited and Base object is set
//
// Resume updates for a suspended pull request on:
//
//   - PullRequestEvent synchronize for the pr branch
//   - PushEvent for it's base-branch
//   - StatusEvent with success state and the StatusCheckRollup is successful
//     and ReviewDecision approved.
//   - CheckRunEvent with a neutral, success or skipped check conclusion and
//     the StatusCheckRollup is successful and ReviewDecision approved.
//   - PullRequestReviewEvent with action submitted and state approved
//
// Trigger update with base-branch on:
//
//   - PushEvent for a base branch
//   - (updates for pull request branches on git-push are triggered via
//     PullRequest synchronize events)
//   - StatusEvent with error or failure state
//   - CheckRunEvent with cancelled, failure, timed_out or action_required
//     conclusion
//
// Other events are ignored and a debug message is logged for those.
func (a *Coordinator) processEvent(ctx context.Context, event *github_prov.Event) {
	defer func() {
		a.processedEventCnt.Add(1)
		metrics.ProcessedEventsInc()
	}()

	logger := a.Logger.With(event.LogFields...)

	switch ev := event.Event.(type) {
	case *github.PullRequestEvent:
		if !a.isMonitoredRepository(ev.GetRepo().GetOwner().GetLogin(), ev.GetRepo().GetName()) {
			logger.Debug("event is for unmonitored repository")
			return
		}

		a.processPullRequestEvent(ctx, logger, ev)

	case *github.PushEvent:
		if !a.isMonitoredRepository(ev.GetRepo().GetOwner().GetLogin(), ev.GetRepo().GetName()) {
			logger.Debug("event is for unmonitored repository")
			return
		}

		a.processPushEvent(ctx, logger, ev)

	case *github.StatusEvent:
		if !a.isMonitoredRepository(ev.GetRepo().GetOwner().GetLogin(), ev.GetRepo().GetName()) {
			logger.Debug("event is for unmonitored repository")
			return
		}

		a.processStatusEvent(ctx, logger, ev)

	case *github.CheckRunEvent:
		if !a.isMonitoredRepository(ev.GetRepo().GetOwner().GetLogin(), ev.GetRepo().GetName()) {
			logger.Debug("event is for unmonitored repository")
			return
		}
		a.processCheckRunEvent(ctx, logger, ev)

	case *github.PullRequestReviewEvent:
		if !a.isMonitoredRepository(ev.GetRepo().GetOwner().GetLogin(), ev.GetRepo().GetName()) {
			logger.Debug("event is for unmonitored repository")
			return
		}

		a.processPullRequestReviewEvent(ctx, logger, ev)

	default:
		logger.Debug("event ignored")
	}
}

func (a *Coordinator) processPullRequestEvent(ctx context.Context, logger *zap.Logger, ev *github.PullRequestEvent) {
	owner := ev.GetRepo().GetOwner().GetLogin()
	repo := ev.GetRepo().GetName()
	baseBranch := ev.GetPullRequest().GetBase().GetRef()

	prNumber := ev.GetNumber()
	// does not have the `refs/heads` prefix:
	branch := ev.GetPullRequest().GetHead().GetRef()

	logger = logger.With(
		logfields.RepositoryOwner(owner),
		logfields.Repository(repo),
		logfields.Branch(branch),
		logfields.PullRequest(prNumber),
		zap.String("github.pull_request_event.action", ev.GetAction()),
	)

	logger.Debug("event received")

	switch action := ev.GetAction(); action {
	case "auto_merge_enabled":
		if !a.TriggerOnAutomerge {
			logger.Debug(
				"event ignored, triggerOnAutomerge is disabled",
			)
			return
		}

		bb, err := NewBaseBranch(owner, repo, baseBranch)
		if err != nil {
			logger.Warn(
				"ignoring event, incomplete base branch information",
				zap.Error(err),
			)
			return
		}

		pr, err := NewPullRequestFromEvent(ev.GetPullRequest())
		if err != nil {
			logger.Warn(
				"ignoring event, incomplete pull request information",
				zap.Error(err),
			)
			return
		}

		if err := a.Enqueue(ctx, bb, pr); err != nil {
			logError(
				logger,
				"ignoring event, could not append pull request to queue",
				err,
			)
			return
		}

		logger.Info(
			"added pull request to merge queue ",
			logfields.Reason("auto_merge_enabled"),
		)

	case "labeled":
		labelName := ev.GetLabel().GetName()
		logger = logger.With(zap.String("github.label_name", labelName))

		if ev.GetPullRequest().GetState() == "closed" {
			logger.Warn(
				"ignoring event, label was added to a closed pull request",
			)
			return
		}

		if labelName == "" {
			logger.Warn(
				"ignoring event, event with action 'labeled' has empty label name",
			)
			return
		}

		if !a.TriggerLabels.Contains(labelName) {
			return
		}

		bb, err := NewBaseBranch(owner, repo, baseBranch)
		if err != nil {
			logger.Warn(
				"ignoring event, incomplete base branch information",
				zap.Error(err),
			)
			return
		}

		pr, err := NewPullRequestFromEvent(ev.GetPullRequest())
		if err != nil {
			logError(
				logger,
				"ignoring event, incomplete pull request information",
				err,
			)
			return
		}

		if err := a.Enqueue(ctx, bb, pr); err != nil {
			logger.Error(
				"ignoring event, enqueing pull request failed",
				zap.Error(err),
			)
			return
		}

		logger.Info(
			"added pull request to merge queue",
			logfields.Reason("labeled"),
		)

	case "auto_merge_disabled":
		if !a.TriggerOnAutomerge {
			logger.Debug(
				"event ignored, triggerOnAutomerge is disabled",
			)
			return
		}

		fallthrough

	case "closed":
		bb, err := NewBaseBranch(owner, repo, baseBranch)
		if err != nil {
			logger.Warn(
				"ignoring event, incomplete base branch information",
				zap.Error(err),
			)
			return
		}

		pr, err := NewPullRequestFromEvent(ev.GetPullRequest())
		if err != nil {
			logger.Warn(
				"ignoring event, incomplete pull request information",
				zap.Error(err),
			)
			return
		}

		pr, err = a.Dequeue(ctx, bb, pr.Number, !ev.PullRequest.GetMerged())
		if err != nil {
			logError(
				logger,
				"removing closed PR from merge queue failed",
				err,
			)
			return
		}

		if ev.PullRequest.GetMerged() {
			metrics.RecordTimeToMerge(time.Since(pr.EnqueuedAt), owner, repo)
		}

		var reason zap.Field
		if action == "closed" {
			reason = logfields.ReasonPRClosed
		} else {
			reason = logfields.Reason(action)
		}

		logger.Info("removed pull request from merge queue", reason)

	case "unlabeled":
		labelName := ev.GetLabel().GetName()
		logger = logger.With(zap.String("github.label_name", labelName))

		if labelName == "" {
			logger.Warn(
				"ignoring event, event with action 'unlabeled' has empty label name",
			)
			return
		}

		if !a.TriggerLabels.Contains(labelName) {
			return
		}

		bb, err := NewBaseBranch(owner, repo, baseBranch)
		if err != nil {
			logger.Warn(
				"ignoring event, incomplete base branch information",
				zap.Error(err),
			)
			return
		}

		pr, err := NewPullRequestFromEvent(ev.GetPullRequest())
		if err != nil {
			logger.Warn(
				"ignoring event, incomplete pull request information",
				zap.Error(err),
			)
			return
		}

		_, err = a.Dequeue(ctx, bb, pr.Number, true)
		if err != nil {
			logError(
				logger,
				"removing PR from merge queue failed",
				err,
			)

			return
		}

		logger.Info(
			"removed pull request from merge queue",
			logfields.Reason("unlabeled"),
		)

	case "synchronize":
		bb, err := NewBaseBranch(owner, repo, baseBranch)
		if err != nil {
			logger.Warn(
				"can not resume PRs, incomplete base branch information",
				zap.Error(err),
			)

			return
		}

		_, err = a.TriggerProcessPRIfFirst(ctx, bb, &PRNumber{Number: prNumber}, TaskTriggerCI)
		if err == nil {
			logger.Info(
				"processing pr for pull request scheduled",
				logfields.Reason("branch_changed"),
			)
			// Resume not necessary, PR is already in the active
			// queue and the first element
			return
		}

		if !errors.Is(err, ErrNotFound) {
			logger.Error("scheduling processing pr for first pr failed", zap.Error(err))
		}

		err = a.Resume(ctx, bb, prNumber)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				logger.Debug("no pull requests suspended for the base branch")
				return
			}

			logger.Error("moving pr from active queued to suspend queue failed", zap.Error(err))
			return
		}

		logger.Info(
			"updates resumed, pr branch changed",
			logfields.Reason("branch_changed"),
		)
		return

	case "edited":
		changes := ev.GetChanges()
		if changes == nil || changes.Base == nil || changes.Base.Ref.From == nil {
			return
		}

		oldBaseBranch, err := NewBaseBranch(owner, repo, *changes.Base.Ref.From)
		if err != nil {
			logger.Warn(
				"ignoring event, incomple old base branch information",
				zap.Error(err),
			)

			return
		}

		bb, err := NewBaseBranch(owner, repo, baseBranch)
		if err != nil {
			logger.Warn(
				"can not enqueue PR after base branch change, base branch information are incomplete",
				zap.Error(err),
			)

			return
		}

		if err := a.ChangeBaseBranch(ctx, oldBaseBranch, bb, prNumber); err != nil {
			if errors.Is(err, ErrNotFound) || errors.Is(err, ErrAlreadyExists) {
				logger.Debug("changing base branch for pr failed failed, pr not qeueud",
					zap.Error(err))
				return
			}

			logger.Error("changing base branch for pr failed", zap.Error(err))
			return
		}

		logger.Info(
			"moved pr to another base branch queue",
			logfields.Reason("base_branch_changed"),
			zap.String("git.old_base_branch", oldBaseBranch.Branch),
			logfields.BaseBranch(bb.Branch),
		)

	default:
		logger.Debug("ignoring irrelevant pull request event")
	}
}

// logError logs an error with the given message and fields.
// If the error is of type ErrNotFound or ErrAlreadyExists the message is
// logged with Info priority, otherwise with Error priority.
func logError(logger *zap.Logger, msg string, err error, fields ...zapcore.Field) {
	fields = append([]zapcore.Field{zap.Error(err)}, fields...)

	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrAlreadyExists) {
		logger.Info(msg, fields...)
		return
	}

	logger.Error(msg, fields...)
}

func (a *Coordinator) processPushEvent(ctx context.Context, logger *zap.Logger, ev *github.PushEvent) {
	// The changed branch can be a base-branch for other
	// PRs or a PR branch
	branch := branchRefToRef(ev.GetRef())
	owner := ev.GetRepo().GetOwner().GetLogin()
	repo := ev.GetRepo().GetName()

	logger = logger.With(
		logfields.Branch(branch),
		logfields.RepositoryOwner(owner),
		logfields.Repository(repo),
	)

	logger.Debug("event received")

	bb, err := NewBaseBranch(owner, repo, branch)
	if err != nil {
		logger.Warn(
			"ignoring event, incomplete branch information",
			zap.Error(err),
		)

		return
	}

	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()

	q, exist := a.queues[bb.BranchID]
	if !exist {
		logger.Debug(
			"ignoring event, queue for base branch does not exist",
			zap.Error(err),
			logfields.BaseBranch(bb.Branch),
		)
		return
	}

	q.ScheduleProcessPR(ctx, TaskTriggerCI)

	for bbID, q := range a.queues {
		if bbID == bb.BranchID {
			q.ResumeAllPRs()
		}
	}
}

func (a *Coordinator) processPullRequestReviewEvent(ctx context.Context, logger *zap.Logger, ev *github.PullRequestReviewEvent) {
	owner := ev.GetRepo().GetOwner().GetLogin()
	repo := ev.GetRepo().GetName()
	prNumber := ev.GetPullRequest().GetNumber()
	branch := ev.GetPullRequest().GetHead().GetRef()
	reviewState := ev.GetReview().GetState()
	action := ev.GetAction()
	submittedAt := ev.GetReview().GetSubmittedAt()

	logger = logger.With(
		logfields.RepositoryOwner(owner),
		logfields.Repository(repo),
		logfields.Branch(branch),
		logfields.PullRequest(prNumber),
		zap.String("github.pull_request.review.state", reviewState),
		zap.String("github.pull_request.review.action", action),
		zap.Time("github.pull_request.review.submitted_at", submittedAt.Time),
	)

	switch reviewState {
	case "approved":
		if action == "submitted" {
			a.ResumeIfStatusPositive(ctx, owner, repo, []string{branch})
			return
		}

		if action != "dismissed" {
			logger.Debug("event ignored, irrelevant action value")
			return
		}

		fallthrough

	case "changes_requested":
		baseBranch := ev.GetPullRequest().GetBase().GetRef()
		bb, err := NewBaseBranch(owner, repo, baseBranch)
		if err != nil {
			logger.Warn(
				"ignoring event, incomplete base branch information",
				zap.Error(err),
			)
			return
		}

		_, err = a.TriggerProcessPRIfFirst(ctx, bb, &PRNumber{Number: prNumber}, TaskNone)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return
			}

			logger.Error("triggering update for first pr failed", zap.Error(err))
			return
		}

		logger.Info(
			"update for pull request triggered",
			logfields.Reason("pr_review_changes_requested"),
		)

		return

	default:
		logger.Debug("event ignored, irrelevant review state")
		return
	}
}

func prBranches(prs []*github.PullRequest) []string {
	res := make([]string, 0, len(prs))

	for _, pr := range prs {
		branch := pr.GetHead().GetRef()
		// should not happen that it is empty
		if branch != "" {
			res = append(res, pr.GetHead().GetRef())
		}
	}

	return res
}

func (a *Coordinator) processCheckRunEvent(ctx context.Context, logger *zap.Logger, ev *github.CheckRunEvent) {
	checkRun := ev.GetCheckRun()
	branches := prBranches(checkRun.PullRequests)
	owner := ev.GetRepo().GetOwner().GetLogin()
	repo := ev.GetRepo().GetName()

	logger = logger.With(
		zap.Strings("git.branches", branches),
		logfields.CheckConclusion(checkRun.GetConclusion()),
		logfields.CheckStatus(checkRun.GetStatus()),
		logfields.RepositoryOwner(owner),
		logfields.Repository(repo),
	)

	logger.Debug("event received")

	if len(branches) == 0 {
		logger.Info("ignoring event, pull request or branches fields is empty")

		return
	}

	for _, pr := range checkRun.PullRequests {
		baseBranch := pr.GetBase().GetRef()
		prNumber := pr.GetNumber()

		logger = logger.With(
			logfields.BaseBranch(baseBranch),
			logfields.PullRequest(prNumber),
		)

		bb, err := NewBaseBranch(owner, repo, baseBranch)
		if err != nil {
			logger.Warn(
				"ignoring check run event for pull-request, incomplete base branch information",
				zap.Error(err),
			)

			err := a.SetPRStaleSinceIfNewer(ctx, bb, pr.GetNumber(), time.Now())
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					logger.Debug(
						"pr not queued, can not update stale timestamp",
					)
					continue
				}
				logger.Error(
					"updating stale timestamp failed",
					zap.Error(err),
				)
			}
		}
	}

	switch checkRun.GetConclusion() {
	case "cancelled", "failure", "timed_out", "action_required":
		for _, branch := range branches {
			pr, err := a.TriggerProcessPRIfFirstAllQueues(ctx, owner, repo, &PRBranch{BranchName: branch})
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					logger.Debug("ignoring checkRun event for branch that is not in merge queue")
				} else {
					logger.Error("triggering update failed", zap.Error(err))
				}

				continue
			}

			logger.With(pr.LogFields...).Info(
				"processPR triggered, negative check run conclusion received",
				logfields.Reason("check_run_result_negative"),
			)
		}

	case "", "neutral", "success", "skipped":
		a.ResumeIfStatusPositive(ctx, owner, repo, branches)

	default: // stale event is ignored
		logger.Info("ignoring event with irrelevant or unsupported check run conclusion")
	}
}

func (a *Coordinator) processStatusEvent(ctx context.Context, logger *zap.Logger, ev *github.StatusEvent) {
	branches := ghBranchesAsStrings(ev.Branches)
	owner := ev.GetRepo().GetOwner().GetLogin()
	repo := ev.GetRepo().GetName()

	logger = logger.With(
		zap.Strings("git.branches", branches),
		logfields.StatusState(ev.GetState()),
		logfields.RepositoryOwner(owner),
		logfields.Repository(repo),
	)

	logger.Debug("event received")

	if len(branches) == 0 {
		logger.Info("ignorning event, pull request or branches field is empty")

		return
	}

	notFound := a.SetPRStaleSinceIfNewerByBranch(ctx, owner, repo, branches, ev.GetUpdatedAt().Time)
	if len(notFound) > 0 {
		logger.Debug(
			"no pr queued for branches, can not update stale timestamp",
			zap.Strings("not_found_branches", notFound),
		)
	}

	switch ev.GetState() {
	case "error", "failure":
		for _, branch := range branches {
			pr, err := a.TriggerProcessPRIfFirstAllQueues(ctx, owner, repo, &PRBranch{BranchName: branch})
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					logger.Debug("processed status event for branch that is not queued for updates")
				} else {
					logger.Error("triggering processPR failed", zap.Error(err))
				}

				continue
			}

			logger.With(pr.LogFields...).Info(
				"processPR triggered, negative status check event received",
				logfields.Reason("status_check_negative"),
			)
		}

	case "pending", "success":
		a.ResumeIfStatusPositive(ctx, owner, repo, branches)

	default:
		logger.Debug("ignoring event with irrelevant or unsupported status")
	}
}

// Enqueue adds the pull request to the mergequeue of baseBranch.
// When it becomes the first element in the queue and it has no negative
// merge-requirements, it will be processed. This means CI jobs will be
// triggered, it is kept up to date with it's base branch, it is labeled and a
// commit status is submitted.
// If the pr is already enqueued an ErrAlreadyExists error is returned.
// If no queue for the baseBranch exist, it will be created.
func (a *Coordinator) Enqueue(_ context.Context, baseBranch *BaseBranch, pr *PullRequest) error {
	var q *queue
	var exist bool

	logger := a.Logger.With(baseBranch.Logfields...).With(pr.LogFields...)

	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()

	q, exist = a.queues[baseBranch.BranchID]
	if !exist {
		q = newQueue(
			baseBranch,
			a.Logger,
			a.GitHubClient,
			a.Retryer,
			a.CI,
			a.HeadLabel,
		)

		a.queues[baseBranch.BranchID] = q
		logger.Debug("queue for base branch created")
	}

	if err := q.Enqueue(pr); err != nil {
		return err
	}

	metrics.EnqueueOpsInc(&baseBranch.BranchID)

	return nil
}

// Dequeue removes the pull request with number prNumber from the queue of
// baseBranch. If no pull request is queued with prNumber an ErrNotFound error
// is returned.
// If the pull request is the only element in the baseBranch queue, the queue
// is removed.
func (a *Coordinator) Dequeue(_ context.Context, baseBranch *BaseBranch, prNumber int, setPendingStatusState bool) (*PullRequest, error) {
	var q *queue
	var exist bool

	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()

	q, exist = a.queues[baseBranch.BranchID]
	if !exist {
		return nil, fmt.Errorf("no queue for base branch exist: %w", ErrNotFound)
	}

	pr, err := q.Dequeue(prNumber, setPendingStatusState)
	if err != nil {
		return nil, fmt.Errorf("removing pr from merge queue failed: %w", err)
	}

	metrics.DequeueOpsInc(&baseBranch.BranchID)

	if q.IsEmpty() {
		q.Wait()
		delete(a.queues, baseBranch.BranchID)

		logger := a.Logger.With(pr.LogFields...).With(baseBranch.Logfields...)

		logger.Debug("removed empty queue for base branch")
	}

	return pr, nil
}

// SetPRStaleSinceIfNewerByBranch sets the staleSince timestamp of the PRs for
// the given branch names to updatdAt, if it is newer then the current
// staleSince timestamp.
// The method returns a list of branch names for that no queued PR could be
// found.
func (a *Coordinator) SetPRStaleSinceIfNewerByBranch(
	_ context.Context,
	owner, repo string,
	branchNames []string,
	updatedAt time.Time,
) (notFound []string) {
	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()

	missing := set.From(branchNames)
	for baseBranch, q := range a.queues {
		if baseBranch.Repository != repo || baseBranch.RepositoryOwner != owner {
			continue
		}

		missing = q.SetPRStaleSinceIfNewerByBranch(branchNames, updatedAt)
	}

	return missing.Slice()
}

// SetPRStaleSinceIfNewer sets the staleSince timestamp of the PR to updatedAt,
// if it is newer then the current staleSince timestamp.
// If the PR is not queued for autoupdates, ErrNotFound is returned.
func (a *Coordinator) SetPRStaleSinceIfNewer(
	_ context.Context,
	baseBranch *BaseBranch,
	prNumber int,
	updatedAt time.Time,
) error {
	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()

	q, exist := a.queues[baseBranch.BranchID]
	if !exist {
		return ErrNotFound
	}

	return q.SetPRStaleSinceIfNewer(prNumber, updatedAt)
}

// ResumeIfStatusPositive schedules processPR for all PRs of branchNames that
// have a positive ready-to-merge status.
func (a *Coordinator) ResumeIfStatusPositive(ctx context.Context, owner, repo string, branchNames []string) {
	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()

	for baseBranch, q := range a.queues {
		if baseBranch.Repository != repo || baseBranch.RepositoryOwner != owner {
			continue
		}

		prs := q.SuspendedPRsbyBranch(branchNames)
		for _, pr := range prs {
			q.ScheduleProcessPRIfStatusPositive(ctx, pr)
		}
	}
}

// Resume moves a pull request from the suspend queue to the active queue.
// If the pull request is not queued for updates ErrNotFound is returned.
func (a *Coordinator) Resume(_ context.Context, baseBranch *BaseBranch, prNumber int) error {
	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()

	q, exist := a.queues[baseBranch.BranchID]
	if !exist {
		return ErrNotFound
	}

	return q.ResumePR(prNumber)
}

// ChangeBaseBranch moves a pull request from the oldBaseBranch queue to the
// one for newBaseBranch.
func (a *Coordinator) ChangeBaseBranch(
	ctx context.Context,
	oldBaseBranch, newBaseBranch *BaseBranch,
	prNumber int,
) error {
	pr, err := a.Dequeue(ctx, oldBaseBranch, prNumber, true)
	if err != nil {
		return fmt.Errorf("could not remove pr from queue for old base branch: %w", err)
	}

	if err := a.Enqueue(ctx, newBaseBranch, pr); err != nil {
		return fmt.Errorf("could not enqueue in queue for new base branch: %w", err)
	}

	return nil
}

// TriggerProcessPRIfFirst schedules the processPR operation for the first pull
// request in the queue if it matches prSpec.
// If an update was triggered, the PullRequest is returned. If the first PR
// does not match prSpec, ErrNotFound is returned.
func (a *Coordinator) TriggerProcessPRIfFirst(
	ctx context.Context,
	baseBranch *BaseBranch,
	prSpec PRSpecifier,
	task Task,
) (*PullRequest, error) {
	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()

	q, exist := a.queues[baseBranch.BranchID]
	if !exist {
		return nil, ErrNotFound
	}

	return a._triggerProcessPRIfFirst(ctx, q, prSpec, task)
}

func (a *Coordinator) _triggerProcessPRIfFirst(
	ctx context.Context,
	q *queue,
	prSpec PRSpecifier,
	task Task,
) (*PullRequest, error) {
	logger := a.Logger.With(q.baseBranch.Logfields...).With(prSpec.LogField())

	// there is a chance of a race here, the pr might not be first anymore
	// when ScheduleUpdateFirstPR() is called, this does not matter, if it
	// happens we run the operation one more time then necessary.
	first := q.FirstActive()
	if first == nil {
		logger.Debug("update not trigger, pr is not first in queue")
		return nil, ErrNotFound
	}

	switch v := prSpec.(type) {
	case *PRNumber:
		if first.Number == v.Number {
			q.ScheduleProcessPR(ctx, task)
			return first, nil
		}

	case *PRBranch:
		if first.Branch == v.BranchName {
			q.ScheduleProcessPR(ctx, task)
			return first, nil
		}

	default:
		logger.DPanic("unsupported type received", zap.String("type", fmt.Sprintf("%T", v)))
		return nil, fmt.Errorf("unsupported type of prSpec parameter: %T", v)
	}

	return nil, ErrNotFound
}

// TriggerProcessPRIfFirstAllQueues does the same then
// _triggerUpdateIfFirst but does not require to specify the base
// branch name.
func (a *Coordinator) TriggerProcessPRIfFirstAllQueues(
	ctx context.Context,
	repoOwner string,
	repo string,
	prSpec PRSpecifier,
) (*PullRequest, error) {
	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()

	for branchID, q := range a.queues {
		if branchID.Repository != repo || branchID.RepositoryOwner != repoOwner {
			continue
		}

		pr, err := a._triggerProcessPRIfFirst(ctx, q, prSpec, TaskNone)
		if err == nil {
			return pr, nil
		}

		if errors.Is(err, ErrNotFound) {
			continue
		}
		return nil, fmt.Errorf("queue: %s: %w", q.String(), err)
	}

	return nil, ErrNotFound
}

func (a *Coordinator) SetPullRequestPriorities(priorities *PRPriorityUpdates) {
	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()
	q, exists := a.queues[priorities.BranchID]
	if !exists {
		return
	}

	var changes int
	for _, update := range priorities.Updates {
		err := q.SetPullRequestPriority(update.PRNumber, update.Priority)
		if err == nil {
			changes++
		}
	}

	if changes > 0 {
		q.SortActiveQueue()
	}
}

func (a *Coordinator) getQueue(branchID *BranchID) *queue {
	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()
	return a.queues[*branchID]
}

// PauseQueue pauses the merge queue.
// Pull request can still be added and removed from the queue but the processPR operation will not run.
func (a *Coordinator) PauseQueue(baseBranch *BranchID) {
	q := a.getQueue(baseBranch)
	if q == nil {
		return
	}

	q.Pause()
}

// Resumes the merge queue.
// Events will be processed again, the processPR operation is run for the first
// PR in the queue.
func (a *Coordinator) ResumeQueue(baseBranch *BranchID) {
	q := a.getQueue(baseBranch)
	if q == nil {
		return
	}

	q.Resume(context.Background())
}

// branchRefToRef returns ref without a leading refs/heads/ prefix.
func branchRefToRef(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}

func ghBranchesAsStrings(branches []*github.Branch) []string {
	result := make([]string, 0, len(branches))

	for _, branch := range branches {
		result = append(result, branch.GetName())
	}

	return result
}
