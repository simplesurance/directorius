// Package githubclt provides a github API client.
package githubclt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v67/github"
	"github.com/shurcooL/githubv4"
	"go.uber.org/zap"
	"golang.org/x/oauth2"

	"github.com/simplesurance/directorius/internal/goorderr"
	"github.com/simplesurance/directorius/internal/logfields"
)

const DefaultHTTPClientTimeout = time.Minute

const loggerName = "github_client"

type StatusState string

const (
	StatusStatePending StatusState = "pending"
	StatusStateSuccess StatusState = "success"
	StatusStateError   StatusState = "error"
	StatusStateFailure StatusState = "failure"
)

// New returns a new github api client.
func New(oauthAPItoken string) *Client {
	httpClient := newHTTPClient(oauthAPItoken)
	return &Client{
		restClt:    github.NewClient(httpClient),
		graphQLClt: githubv4.NewClient(httpClient),
		logger:     zap.L().Named(loggerName),
	}
}

func newHTTPClient(apiToken string) *http.Client {
	if apiToken == "" {
		return &http.Client{
			Timeout: DefaultHTTPClientTimeout,
		}
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: apiToken},
	)

	tc := oauth2.NewClient(context.Background(), ts)
	tc.Timeout = DefaultHTTPClientTimeout

	return tc
}

// Client is an github API client.
// All methods return a goorderr.RetryableError when an operation can be retried.
// This can be e.g. the case when the API ratelimit is exceeded.
type Client struct {
	restClt    *github.Client
	graphQLClt *githubv4.Client
	logger     *zap.Logger
}

// BranchIsBehindBase returns true if the head reference contains all changes of base.
func (clt *Client) BranchIsBehindBase(ctx context.Context, owner, repo, base, head string) (behind bool, err error) {
	cmp, _, err := clt.restClt.Repositories.CompareCommits(ctx, owner, repo, base, head, &github.ListOptions{PerPage: 1})
	if err != nil {
		return false, clt.wrapRESTRetryableErrors(err)
	}

	if cmp.BehindBy == nil {
		return false, goorderr.NewRetryableAnytimeError(errors.New("github returned a nil BehindBy field"))
	}

	return *cmp.BehindBy > 0, nil
}

// PullRequestIsUptodateWithBase returns true if the pull request is open and
// contains all changes from it's base branch.
// Additionally it returns the SHA of the head commit for which the status was
// checked.
// If the PR is closed PullRequestIsClosedError is returned.
func (clt *Client) PRIsUptodate(ctx context.Context, owner, repo string, pullRequestNumber int) (isUptodate bool, headSHA string, err error) {
	pr, _, err := clt.restClt.PullRequests.Get(ctx, owner, repo, pullRequestNumber)
	if err != nil {
		return false, "", clt.wrapRESTRetryableErrors(err)
	}

	if pr.GetState() == "closed" {
		return false, "", ErrPullRequestIsClosed
	}

	prHead := pr.GetHead()
	if prHead == nil {
		return false, "", errors.New("got pull request object with empty head")
	}

	prHeadSHA := prHead.GetSHA()
	if prHeadSHA == "" {
		return false, "", errors.New("got pull request object with empty head sha")
	}

	if pr.GetMergeableState() == "behind" {
		return false, prHeadSHA, nil
	}

	prBranch := prHead.GetRef()
	if prBranch == "" {
		return false, "", errors.New("got pull request object with empty ref field")
	}

	base := pr.GetBase()
	if base == nil {
		return false, "", errors.New("got pull request object with empty base field")
	}

	baseBranch := base.GetRef()
	if baseBranch == "" {
		return false, "", errors.New("got pull request object with empty base ref field")
	}

	isBehind, err := clt.BranchIsBehindBase(ctx, owner, repo, baseBranch, prHeadSHA)
	if err != nil {
		return false, "", fmt.Errorf("evaluating if branch is behind base failed: %w", err)
	}

	return !isBehind, prHeadSHA, nil
}

// CreateIssueComment creates a comment in a issue or pull request
func (clt *Client) CreateIssueComment(ctx context.Context, owner, repo string, issueOrPRNr int, comment string) error {
	_, _, err := clt.restClt.Issues.CreateComment(ctx, owner, repo, issueOrPRNr, &github.IssueComment{Body: &comment})
	return clt.wrapRESTRetryableErrors(err)
}

type UpdateBranchResult struct {
	Changed      bool
	Scheduled    bool
	HeadCommitID string
}

// UpdateBranch schedules merging the base-branch into a pull request branch.
// If the PR contains all changes of it's base branch, false is returned for changed
// If it's not up-to-date and updating the PR was scheduled at github, true is returned for changed and scheduled.
// If the PR was updated while the method was executed, a
// goorderr.RetryableError is returned and the operation can be retried.
// If the branch can not be updated automatically because of a merge conflict,
// an [ErrMergeConflict] is returned
func (clt *Client) UpdateBranch(ctx context.Context, owner, repo string, pullRequestNumber int) (*UpdateBranchResult, error) {
	// 1. Get Commit of PR
	// 2. Check if it is up-to-date
	// 3. If not -> Update, specify commit as HEAD branch
	// 4. "expected head sha didn’t match current head ref" -> try again

	// If UpdateBranch is called and the branch is already
	// up-to-date, github creates an empty merge commit and changes
	// the branch. Therefore we have to check first if an update is
	// needed.
	isUptodate, prHEADSHA, err := clt.PRIsUptodate(ctx, owner, repo, pullRequestNumber)
	if err != nil {
		return nil, fmt.Errorf("evaluating if PR is up-to-date with base branch failed: %w", err)
	}

	logger := clt.logger.With(
		logfields.RepositoryOwner(owner),
		logfields.Repository(repo),
		logfields.PullRequest(pullRequestNumber),
		logfields.Commit(prHEADSHA),
	)

	if isUptodate {
		logger.Debug("branch is up-to-date with base branch, skipping running update branch operation")
		return &UpdateBranchResult{HeadCommitID: prHEADSHA}, nil
	}

	_, _, err = clt.restClt.PullRequests.UpdateBranch(ctx, owner, repo, pullRequestNumber, &github.PullRequestBranchUpdateOptions{ExpectedHeadSHA: &prHEADSHA})
	if err != nil {
		if _, ok := err.(*github.AcceptedError); ok { // nolint:errorlint // errors.As not needed here
			// It is not clear if the response ensures that the
			// branch will be updated or if the scheduled operation
			// can fail.
			logger.Debug("updating branch with base branch scheduled")
			return &UpdateBranchResult{Scheduled: true, Changed: true}, nil
		}

		var respErr *github.ErrorResponse
		if errors.As(err, &respErr) {
			if respErr.Response.StatusCode == http.StatusUnprocessableEntity {
				if strings.Contains(respErr.Message, "merge conflict") {
					return nil, NewErrMergeConflict(err)
				}

				if strings.Contains(respErr.Message, "expected head sha didn’t match current head ref") {
					logger.Debug("branch changed while trying to sync with base branch")

					return nil, goorderr.NewRetryableAnytimeError(err)
				}

				if strings.Contains(respErr.Message, "no new commits on the base branch") {
					logger.Debug("branch is already up-to-date with base branch, considering update successful")
					return &UpdateBranchResult{HeadCommitID: prHEADSHA}, nil
				}
			}
		}

		return nil, clt.wrapRESTRetryableErrors(err)
	}

	logger.Debug("branch was updated with base branch")
	// github seems to always schedule update operations and return an
	// AcceptedError, this condition might never happened
	return &UpdateBranchResult{Changed: true, HeadCommitID: prHEADSHA}, nil
}

// AddLabel adds a label to Pull-Request or Issue.
func (clt *Client) AddLabel(ctx context.Context, owner, repo string, pullRequestOrIssueNumber int, label string) error {
	if label == "" {
		// by default github removes all labels when none is provided,
		// we do not need this functionality, as safe guard fail if
		// because of a bug an empty label value is passed:
		return errors.New("provided label is empty")
	}
	_, _, err := clt.restClt.Issues.AddLabelsToIssue(ctx, owner, repo, pullRequestOrIssueNumber, []string{label})
	return clt.wrapRESTRetryableErrors(err)
}

// RemoveLabel removes a label from a Pull-Request or issue.
// If the issue or PR does not have the label, the operation succeeds.
func (clt *Client) RemoveLabel(ctx context.Context, owner, repo string, pullRequestOrIssueNumber int, label string) error {
	_, err := clt.restClt.Issues.RemoveLabelForIssue(
		ctx,
		owner,
		repo,
		pullRequestOrIssueNumber,
		label,
	)
	if err != nil {
		var respErr *github.ErrorResponse
		if errors.As(err, &respErr) {
			if respErr.Response.StatusCode == http.StatusNotFound {
				clt.logger.Debug("removing label returned a not found response, interpreting it as success",
					logfields.RepositoryOwner(owner),
					logfields.Repository(repo),
					logfields.PullRequest(pullRequestOrIssueNumber),
					logfields.Label(label),
					zap.Error(err),
				)

				return nil
			}

			return clt.wrapGraphQLRetryableErrors(err)
		}
	}

	return nil
}

// CreateCommitStatus submits a status for a commit.
// state must be one of [StatusStatePending], [StatusStateSuccess], [StatusStateError], [StatusStateFailure].
func (clt *Client) CreateCommitStatus(ctx context.Context, owner, repo, commit string, state StatusState, description, context string) error {
	// The GraphQL API returns states in uppercase, the REST API wants
	// lowercase states according to the documentation:
	s := string(state)
	_, _, err := clt.restClt.Repositories.CreateStatus(ctx, owner, repo, commit, &github.RepoStatus{
		State:       &s,
		Description: &description,
		Context:     &context,
	})
	return clt.wrapRESTRetryableErrors(err)
}

// CreateCommitStatus submits a status for the HEAD commit of a pull request branch.
// state must be one of [StatusStatePending], [StatusStateSuccess], [StatusStateError], [StatusStateFailure].
func (clt *Client) CreateHeadCommitStatus(ctx context.Context, owner, repo string, pullRequestNumber int, state StatusState, description, context string) error {
	pr, _, err := clt.restClt.PullRequests.Get(ctx, owner, repo, pullRequestNumber)
	if err != nil {
		return clt.wrapRESTRetryableErrors(err)
	}

	prHead := pr.GetHead()
	if prHead == nil {
		return errors.New("got pull request object with empty head")
	}

	prHeadSHA := prHead.GetSHA()
	if prHeadSHA == "" {
		return errors.New("got pull request object with empty head sha")
	}

	return clt.CreateCommitStatus(ctx, owner, repo, prHeadSHA, state, description, context)
}
