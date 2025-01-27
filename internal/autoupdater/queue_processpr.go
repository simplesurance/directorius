package autoupdater

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/jenkins"
	"github.com/simplesurance/directorius/internal/logfields"
)

type Action int

const (
	ActionUndefined Action = iota
	ActionNone
	ActionSuspend
	ActionCreatePullRequestComment
	ActionUpdateStateUnchanged
	ActionCreateSuccessfulGithubStatus
	ActionWaitForMerge
	ActionAddFirstInQueueGithubLabel
	ActionTriggerCIJobs
)

type requiredActions struct {
	Actions      []Action
	Reason       string
	HeadCommitID string

	// ExpectedCIRuns is only set when ActionTriggerCIJobs is part of Actions.
	ExpectedCIRuns []string
}

func (a Action) String() string {
	switch a {
	case ActionUndefined:
		return "undefined"
	case ActionNone:
		return "none"
	case ActionSuspend:
		return "suspend"
	case ActionCreatePullRequestComment:
		return "create pull request comment"
	case ActionUpdateStateUnchanged:
		return "update unchanged-state"
	case ActionCreateSuccessfulGithubStatus:
		return "create successful github status"
	case ActionWaitForMerge:
		return "wait for merge"
	case ActionAddFirstInQueueGithubLabel:
		return "add first-in-queue github label"
	case ActionTriggerCIJobs:
		return "trigger ci jobs"
	default:
		return strconv.Itoa(int(a))
	}
}

func (q *queue) evalPRAction(ctx context.Context, logger *zap.Logger, pr *PullRequest) (*requiredActions, error) {
	const (
		reasonNotApproved           = "not approved"
		reasonPullRequestClosed     = "pull request is closed"
		reasonMergeConflict         = "update with base branch failed: merge conflict"
		reasonBranchUpdatedWithBase = "pull request branch has been updated with base branch"
		reasonIsStale               = "pull request is stale"
		reasonCIStatusExpected      = "ci jobs not running"
		reasonCIStatusPending       = "ci jobs running"
		reasonCIStatusFailure       = "ci jobs failed"
		reasonPreviousCIJobsFailed  = "overall ci job status is negative but none of the last triggered jobs failed"
		reasonMergeRequirementMet   = "merge requirements are met"
	)

	status, err := q.prReadyForMergeStatus(ctx, pr)
	if err != nil {
		return nil, fmt.Errorf("retrieving ready to merge status from github failed: %w", err)
	}

	if status.ReviewDecision != githubclt.ReviewDecisionApproved {
		return &requiredActions{
				Actions: []Action{ActionSuspend},
				Reason:  reasonNotApproved,
			},
			nil
	}

	logger.Debug("pull request is approved")

	branchChanged, updateHeadCommit, err := q.updatePRWithBase(ctx, pr)
	if err != nil {
		if errors.Is(err, ErrPullRequestIsClosed) {
			return &requiredActions{
				Actions: []Action{ActionSuspend},
				Reason:  reasonPullRequestClosed,
			}, nil
		}

		var errMergeConflict *githubclt.ErrMergeConflict
		if errors.As(err, &errMergeConflict) {
			return &requiredActions{
				Actions:      []Action{ActionCreatePullRequestComment, ActionSuspend},
				Reason:       reasonMergeConflict + ": " + err.Error(),
				HeadCommitID: status.Commit,
			}, nil
		}

		return nil, err
	}

	if branchChanged {
		logger.Info("branch updated with changes from base branch",
			logfields.Commit(updateHeadCommit))
		// queue-head label is not added neither CI jobs are triggered ,
		// the update of the branch will cause a PullRequest
		// synchronize event, that will trigger another run of this
		// function which will then trigger the CI jobs and add the
		// label.
		return &requiredActions{
			Actions:      []Action{ActionUpdateStateUnchanged},
			Reason:       reasonBranchUpdatedWithBase,
			HeadCommitID: updateHeadCommit,
		}, nil

	}

	if q.isPRStale(pr) {
		return &requiredActions{
			Actions:      []Action{ActionSuspend},
			Reason:       reasonIsStale + "since: " + pr.GetStateUnchangedSince().Round(time.Second).String(),
			HeadCommitID: status.Commit,
		}, nil
	}

	logger.Debug("pull request is not stale",
		zap.Time("last_pr_status_change", pr.GetStateUnchangedSince()),
		zap.Duration("stale_timeout", q.staleTimeout),
	)

	if status.Commit != updateHeadCommit {
		logger.Warn("retrieved ready for merge status for a different "+
			"commit than the current head commit according to "+
			"the update branch operation, branch might have "+
			"changed in between or github might have returned "+
			"outdated information",
			zap.String("github.ready_for_merge_commit", status.Commit),
			zap.String("github.update_branch_head_commit", updateHeadCommit),
		)
		// TODO: rerun the update loop, instead of continuing with the wrong information!
	}

	switch status.CIStatus {
	case githubclt.CIStatusSuccess:
		return &requiredActions{
			Actions:      []Action{ActionCreateSuccessfulGithubStatus, ActionNone},
			Reason:       reasonMergeRequirementMet,
			HeadCommitID: updateHeadCommit,
		}, nil

	case githubclt.CIStatusPending:
		return &requiredActions{
			Actions: []Action{
				ActionAddFirstInQueueGithubLabel,
				ActionCreateSuccessfulGithubStatus,
			},
			Reason:       reasonCIStatusPending,
			HeadCommitID: updateHeadCommit,
		}, nil

	case githubclt.CIStatusExpected:
		return &requiredActions{
			Actions: []Action{
				ActionAddFirstInQueueGithubLabel,
				ActionTriggerCIJobs,
				ActionCreateSuccessfulGithubStatus,
			},
			Reason:         reasonCIStatusExpected,
			HeadCommitID:   updateHeadCommit,
			ExpectedCIRuns: expectedCIJobs(status.Statuses),
		}, nil

	case githubclt.CIStatusFailure:
		if len(status.Statuses) == 0 {
			return nil, errors.New("ready for merge status has negative CI status but statuses list is empty")
		}

		failedStatusesAreObsolete, err := q.failedRequiredCIStatusesAreObsolete(
			status.Statuses,
			pr.GetLastStartedCIBuilds(),
		)
		if err != nil {
			return nil, err
		}

		if failedStatusesAreObsolete {
			logger.Debug("status check is negative "+
				"but none of the affected ci builds are in the list "+
				"of recently triggered required jobs, pr is not suspended",
				zap.Any("ci.build.last_triggered", pr.lastStartedCIBuilds),
				zap.Any("github.ci_statuses", status),
			)

			return &requiredActions{
				Actions:      []Action{ActionNone},
				Reason:       reasonPreviousCIJobsFailed,
				HeadCommitID: updateHeadCommit,
			}, nil
		}

		return &requiredActions{
			Actions:      []Action{ActionSuspend},
			Reason:       reasonCIStatusFailure,
			HeadCommitID: updateHeadCommit,
		}, nil

	default:
		logger.DPanic("BUG: pull request ci status has unexpected value")
		return nil, fmt.Errorf("BUG: pull request ci status has unexpected value: %q", status.CIStatus)
	}
}

func failedRequiredStatuses(statuses []*githubclt.CIJobStatus) []*githubclt.CIJobStatus {
	var result []*githubclt.CIJobStatus

	for _, s := range statuses {
		if s.Required && s.Status == githubclt.CIStatusFailure {
			result = append(result, s)
		}
	}

	return result
}

// failedRequiredCIStatusesAreObsolete returns true when for each failed and
// required status an entry in lastStartedCIBuilds exist that has a higher
// build number.
func (q *queue) failedRequiredCIStatusesAreObsolete(statuses []*githubclt.CIJobStatus, lastStartedCIBuilds map[string]*jenkins.Build) (bool, error) {
	for _, s := range failedRequiredStatuses(statuses) {
		failedBuild, err := jenkins.ParseBuildURL(s.JobURL)
		if err != nil {
			return false, fmt.Errorf("parsing ci job url (%q) as jenkins build url failed: %w", s.JobURL, err)
		}

		lastBuild, exist := lastStartedCIBuilds[failedBuild.JobName]
		if !exist {
			q.logger.Debug("ci job status is for job that has not been triggered",
				logfields.CIJob(failedBuild.JobName),
				zap.Stringer("ci.latest_build", lastBuild),
				zap.Stringer("github.ci_failed_status_build", failedBuild),
			)
			return false, nil
		}

		if failedBuild.Number >= lastBuild.Number {
			q.logger.Debug("failed ci job status is for latest or newer build",
				logfields.CIJob(failedBuild.JobName),
				zap.Stringer("ci.latest_build", lastBuild),
				zap.Stringer("github.ci_failed_status_build", failedBuild),
			)
			return false, nil
		}
	}

	return true, nil
}

func expectedCIJobs(statuses []*githubclt.CIJobStatus) (result []string) {
	for _, s := range statuses {
		if s.Status == githubclt.CIStatusExpected {
			result = append(result, s.Name)
		}
	}
	return result
}
