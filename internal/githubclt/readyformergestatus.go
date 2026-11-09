package githubclt

import (
	"context"
	"fmt"
	"time"

	"github.com/shurcooL/githubv4"
	"go.uber.org/zap"
)

// CIStatus is the result or current status of a CI job.
type CIStatus string

const (
	// CIStatusExpected means that the execution of the job is outstanding.
	CIStatusExpected CIStatus = "EXPECTED"
	// CIStatusPending is the status when a job is currently running.
	CIStatusPending CIStatus = "PENDING"
	CIStatusSuccess CIStatus = "SUCCESS"
	CIStatusFailure CIStatus = "FAILURE"
)

// ReviewDecision is the result of a pull request review.
type ReviewDecision string

const (
	ReviewDecisionApproved         = ReviewDecision(githubv4.PullRequestReviewDecisionApproved)
	ReviewDecisionChangesRequested = ReviewDecision(githubv4.PullRequestReviewDecisionChangesRequested)
	ReviewDecisionReviewRequired   = ReviewDecision(githubv4.PullRequestReviewDecisionReviewRequired)
)

// CIJobStatus is the status of a CI job.
// It represents the status of GitHub CheckRuns and Commit statuses.
type CIJobStatus struct {
	Name     string
	Status   CIStatus
	Required bool
	JobURL   string
}

// ReadyForMergeStatus represent the information deciding if a pull request is
// ready to be merged. It contains the review decision and CI job results.
// It also contains the information that determines if the pull request is
// still eligible to be in a merge queue: if it is open, if auto-merge is
// enabled and its labels.
type ReadyForMergeStatus struct {
	ReviewDecision ReviewDecision
	CIStatus       CIStatus
	Statuses       []*CIJobStatus
	Commit         string

	// Closed is true if the pull request is closed or merged.
	Closed bool
	// AutoMergeEnabled is true if auto-merge is enabled for the pull
	// request.
	AutoMergeEnabled bool
	// Labels contains the names of the labels of the pull request.
	// At most the first [maxLabels] labels are retrieved.
	Labels []string
}

// maxLabels is the max. number of pull request labels that are retrieved.
// It must be kept in sync with the labels(first: N) argument in the query.
const maxLabels = 100

// ReadyForMerge returns the [review decision] and [status check rollup] for a PR.
// It additionally returns if the PR is closed, if auto-merge is enabled and the
// labels of the PR.
//
// The status of checks that are not required is ignored.
// If a required check failed the returned [ReadyForMerge.CIStatus] is
// [CIStatusFailure].
// If a check or status has not started yet but is required, it is [CIStatusExpected].
// If a check or status is running and all others succeeded or also still it is [CIStatusPending].
// It is [CIStatusSuccess], if no check or status is in pending state and all
// required ones succeeded.
//
// [status check rollup]: https://docs.github.com/en/graphql/reference/objects#statuscheckrollup
// [review decision]: https://docs.github.com/en/graphql/reference/enums#pullrequestreviewdecision
func (clt *Client) ReadyForMerge(ctx context.Context, owner, repo string, prNumber int) (*ReadyForMergeStatus, error) {
	queryResult, err := clt.reviewAndCIStatus(ctx, owner, repo, prNumber)
	if err != nil {
		return nil, clt.wrapGraphQLRetryableErrors(err)
	}

	statuses, err := toCIJobStatuses(queryResult.RequiredStatusCheckContexts, queryResult.CheckRuns, queryResult.StatusContext)
	if err != nil {
		return nil, err
	}
	return &ReadyForMergeStatus{
		ReviewDecision: ReviewDecision(queryResult.ReviewDecision),
		CIStatus:       overallCIStatus(queryResult.StatusCheckRollupState, statuses),
		Statuses:       statuses,
		Commit:         queryResult.Commit,

		Closed:           queryResult.Closed,
		AutoMergeEnabled: queryResult.AutoMergeEnabled,
		Labels:           queryResult.Labels,
	}, nil
}

func overallCIStatus(statusCheckRollupState githubv4.StatusState, statuses []*CIJobStatus) CIStatus {
	required := requiredCIStatus(statuses)
	// A missing required check wins over the rollup state. The rollup
	// only covers checks that exist, so an optional check that is still
	// running would otherwise hide that the required CI jobs were never
	// triggered.
	if required == CIStatusExpected {
		return CIStatusExpected
	}

	switch statusCheckRollupState {
	case githubv4.StatusStatePending:
		return CIStatusPending
	case githubv4.StatusStateExpected:
		return CIStatusExpected
	}

	return required
}

func requiredCIStatus(statuses []*CIJobStatus) CIStatus {
	result := CIStatusSuccess
	for _, status := range statuses {
		if !status.Required {
			continue
		}

		switch status.Status {
		case CIStatusExpected:
			result = CIStatusExpected

		case CIStatusPending:
			if result != CIStatusExpected {
				result = CIStatusPending
			}

		case CIStatusFailure:
			return CIStatusFailure
		}
	}

	return result
}

func toCIJobStatuses(
	requiredChecks []string,
	checkRuns []*queryCheckStatus,
	commitStatuses []*queryStatusContext,
) ([]*CIJobStatus, error) {
	statusesByName := make(map[string]*CIJobStatus, len(checkRuns)+len(commitStatuses)+len(requiredChecks))
	for _, context := range requiredChecks {
		if _, exists := statusesByName[context]; exists {
			return nil, fmt.Errorf("found 2 required status with the same context values: %q, context values must be unique", context)
		}

		statusesByName[context] = &CIJobStatus{
			Name:     context,
			Status:   CIStatusExpected,
			Required: true,
		}
	}

	for _, run := range checkRuns {
		status, err := checkRunResultToCiStatus(run.Status, run.Conclusion)
		if err != nil {
			return nil, fmt.Errorf("converting checkRun %q CIstatus failed: %w", run.Name, err)
		}

		if entry, exists := statusesByName[run.Name]; exists {
			entry.Status = status
			entry.JobURL = run.DetailsURL
			continue
		}

		statusesByName[run.Name] = &CIJobStatus{
			Name:     run.Name,
			Status:   status,
			Required: false,
			JobURL:   run.DetailsURL,
		}
	}

	for _, commitStatus := range commitStatuses {
		status, err := contextStatusStateToCIStatus(commitStatus.State)
		if err != nil {
			return nil, fmt.Errorf("converting %q status context to CIstatus failed: %w",
				commitStatus.Context, err)
		}
		if entry, exists := statusesByName[commitStatus.Context]; exists {
			entry.Status = status
			entry.JobURL = commitStatus.TargetURL
			continue
		}

		statusesByName[commitStatus.Context] = &CIJobStatus{
			Name:     commitStatus.Context,
			Status:   status,
			Required: false,
			JobURL:   commitStatus.TargetURL,
		}
	}

	result := make([]*CIJobStatus, 0, len(statusesByName))
	for _, status := range statusesByName {
		result = append(result, status)
	}

	return result, nil
}

func checkRunResultToCiStatus(status githubv4.CheckStatusState, conclusion githubv4.CheckConclusionState) (CIStatus, error) {
	switch status {
	case githubv4.CheckStatusStateInProgress,
		githubv4.CheckStatusStatePending,
		githubv4.CheckStatusStateQueued,
		githubv4.CheckStatusStateRequested,
		githubv4.CheckStatusStateWaiting:
		return CIStatusPending, nil

	case githubv4.CheckStatusStateCompleted:
		return checkConclusiontoCIStatus(conclusion)

	default:
		return "", fmt.Errorf("unsupported status value: %q", status)
	}
}

func checkConclusiontoCIStatus(conclusion githubv4.CheckConclusionState) (CIStatus, error) {
	switch conclusion {
	case githubv4.CheckConclusionStateCancelled,
		githubv4.CheckConclusionStateFailure,
		githubv4.CheckConclusionStateStale,
		githubv4.CheckConclusionStateStartupFailure,
		githubv4.CheckConclusionStateTimedOut:
		return CIStatusFailure, nil

	case githubv4.CheckConclusionStateActionRequired:
		return CIStatusPending, nil

	case githubv4.CheckConclusionStateNeutral,
		githubv4.CheckConclusionStateSkipped,
		githubv4.CheckConclusionStateSuccess:
		return CIStatusSuccess, nil
	default:
		return "", fmt.Errorf("unsupported conclusion value: %q", conclusion)
	}
}

type queryCheckStatus struct {
	Name       string
	Conclusion githubv4.CheckConclusionState
	Status     githubv4.CheckStatusState
	DetailsURL string `graphql:"detailsUrl"`
}

type queryStatusContext struct {
	State     githubv4.StatusState
	Context   string
	TargetURL string `graphql:"targetUrl"`
}

type queryCIStatusResult struct {
	ReviewDecision              githubv4.PullRequestReviewDecision
	StatusCheckRollupState      githubv4.StatusState
	RequiredStatusCheckContexts []string
	CheckRuns                   []*queryCheckStatus
	StatusContext               []*queryStatusContext
	Commit                      string
	Closed                      bool
	AutoMergeEnabled            bool
	Labels                      []string
}

func (clt *Client) reviewAndCIStatus(ctx context.Context, owner, repo string, prNumber int) (*queryCIStatusResult, error) {
	type graphQLQueryCIStatus struct {
		Repository struct {
			PullRequest struct {
				ReviewDecision githubv4.PullRequestReviewDecision
				// Closed is also true for merged pull requests.
				Closed           bool
				AutoMergeRequest struct {
					EnabledAt time.Time
				}
				Labels struct {
					PageInfo PageInfo
					Nodes    []struct {
						Name string
					}
				} `graphql:"labels(first: 100)"`

				BaseRef struct {
					BranchProtectionRule struct {
						// RequiredStatusCheckContexts
						// contains required commit
						// statuses and checkRuns.
						RequiredStatusCheckContexts []string
					}
				}

				Commits struct {
					Nodes []struct {
						Commit struct {
							Oid               string
							StatusCheckRollup struct {
								State    githubv4.StatusState
								Contexts struct {
									PageInfo PageInfo
									Edges    []struct {
										Node struct {
											CheckRun      queryCheckStatus   `graphql:"... on CheckRun"`
											StatusContext queryStatusContext `graphql:"... on StatusContext"`
										}
									}
								} `graphql:"contexts(first: $contextsFirst, after: $contextsAfter)"`
							}
						}
					}
				} `graphql:"commits(last: $commitsLast)"`
			} `graphql:"pullRequest(number: $number)"`
		} `graphql:"repository(owner: $owner, name: $name)"`
	}

	var prHEADCommitID string
	var result queryCIStatusResult

	vars := map[string]any{
		"owner":         githubv4.String(owner),
		"name":          githubv4.String(repo),
		"number":        githubv4.Int(prNumber),
		"commitsLast":   githubv4.Int(1),
		"contextsFirst": githubv4.Int(100),
		"contextsAfter": (*githubv4.String)(nil),
	}
	page := -1
	for {
		page++
		var q graphQLQueryCIStatus

		err := clt.graphQLClt.Query(ctx, &q, vars)
		if err != nil {
			return nil, err
		}

		commitsNode := q.Repository.PullRequest.Commits.Nodes[0].Commit

		if prHEADCommitID == "" {
			prHEADCommitID = commitsNode.Oid
		} else if prHEADCommitID != commitsNode.Oid {
			vars["contextsAfter"] = (*githubv4.String)(nil)
			prHEADCommitID = ""

			continue
		}

		for _, edge := range commitsNode.StatusCheckRollup.Contexts.Edges {
			node := edge.Node
			if node.CheckRun.Name != "" && node.StatusContext.Context != "" {
				return nil, fmt.Errorf("internal error: node contains checkRun and context, expecting only one")
			}

			if node.CheckRun.Name != "" {
				result.CheckRuns = append(result.CheckRuns, &node.CheckRun)
				continue
			}

			result.StatusContext = append(result.StatusContext, &node.StatusContext)
		}

		pageInfo := commitsNode.StatusCheckRollup.Contexts.PageInfo
		if !pageInfo.HasNextPage {
			result.ReviewDecision = q.Repository.PullRequest.ReviewDecision
			result.StatusCheckRollupState = commitsNode.StatusCheckRollup.State
			result.RequiredStatusCheckContexts = q.Repository.PullRequest.BaseRef.BranchProtectionRule.RequiredStatusCheckContexts
			result.Commit = prHEADCommitID
			result.Closed = q.Repository.PullRequest.Closed
			result.AutoMergeEnabled = !q.Repository.PullRequest.AutoMergeRequest.EnabledAt.IsZero()
			result.Labels = make([]string, 0, len(q.Repository.PullRequest.Labels.Nodes))
			for _, label := range q.Repository.PullRequest.Labels.Nodes {
				result.Labels = append(result.Labels, label.Name)
			}

			if q.Repository.PullRequest.Labels.PageInfo.HasNextPage {
				clt.logger.Warn("incomplete pr labels retrieved, more label pages are available but only the first page was fetched, pagination is not implemented",
					zap.Int("github.pull_request", prNumber),
					zap.Int("github.labels_fetched", maxLabels),
				)
			}

			return &result, nil
		}

		if pageInfo.EndCursor == "" {
			return nil, fmt.Errorf("retrieving all contexts failed, HasNextPage is %s, expected non-empty EndCursor", pageInfo.EndCursor)
		}

		vars["contextsAfter"] = pageInfo.EndCursor
	}
}

func contextStatusStateToCIStatus(state githubv4.StatusState) (CIStatus, error) {
	switch state {
	case githubv4.StatusStateError,
		githubv4.StatusStateFailure:
		return CIStatusFailure, nil

	case githubv4.StatusStateExpected:
		return CIStatusExpected, nil

	case githubv4.StatusStatePending:
		return CIStatusPending, nil

	case githubv4.StatusStateSuccess:
		return CIStatusSuccess, nil

	default:
		return "", fmt.Errorf("unsupported status state value: %q", state)
	}
}
