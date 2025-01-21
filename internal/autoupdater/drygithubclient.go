package autoupdater

import (
	"context"
	"iter"

	"go.uber.org/zap"

	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/logfields"
)

const dryGitHubClientHeadCommitID = "32d4ff96ea72412277bbfd22ff1bab3a5263b415"

// DryGithubClient is a github-client that does not do any changes on github.
// All operations that could cause a change are simulated and always succeed.
// All all other operations are forwarded to a wrapped GithubClient.
type DryGithubClient struct {
	clt    GithubClient
	logger *zap.Logger
}

func NewDryGithubClient(clt GithubClient, logger *zap.Logger) *DryGithubClient {
	return &DryGithubClient{
		clt:    clt,
		logger: logger.Named("dry_github_client"),
	}
}

func (c *DryGithubClient) UpdateBranch(context.Context, string, string, int) (*githubclt.UpdateBranchResult, error) {
	c.logger.Info("simulated updating github branch, returning is uptodate")
	return &githubclt.UpdateBranchResult{HeadCommitID: dryGitHubClientHeadCommitID}, nil
}

func (c *DryGithubClient) ReadyForMerge(context.Context, string, string, int) (*githubclt.ReadyForMergeStatus, error) {
	c.logger.Info("simulated fetching ready for merge status, pr is approved, all checks successful")

	return &githubclt.ReadyForMergeStatus{
		ReviewDecision: githubclt.ReviewDecisionApproved,
		CIStatus:       githubclt.CIStatusSuccess,
		Commit:         dryGitHubClientHeadCommitID,
	}, nil
}

func (c *DryGithubClient) CreateIssueComment(context.Context, string, string, int, string) error {
	c.logger.Info("simulated creation of github issue comment, no comment created on github")
	return nil
}

func (c *DryGithubClient) AddLabel(_ context.Context, owner, repo string, pullRequestOrIssueNumber int, label string) error {
	c.logger.Info("simulated adding label to pull request",
		logfields.RepositoryOwner(owner),
		logfields.Repository(repo),
		logfields.PullRequest(pullRequestOrIssueNumber),
		logfields.Label(label),
	)

	return nil
}

func (c *DryGithubClient) RemoveLabel(_ context.Context, owner, repo string, pullRequestOrIssueNumber int, label string) error {
	c.logger.Info("simulated removing label from pull request",
		logfields.RepositoryOwner(owner),
		logfields.Repository(repo),
		logfields.PullRequest(pullRequestOrIssueNumber),
		logfields.Label(label),
	)
	return nil
}

func (c *DryGithubClient) CreateCommitStatus(_ context.Context, owner, repo, commit string, state githubclt.StatusState, description, context string) error {
	c.logger.Info("simulated creating of github commit status, status has not been created on github",
		logfields.RepositoryOwner(owner),
		logfields.Repository(repo),
		logfields.Commit(commit),
		zap.String("github.status.state", string(state)),
		zap.String("github.status.description", description),
		zap.String("github.status.context", context),
	)

	return nil
}

func (c *DryGithubClient) CreateHeadCommitStatus(_ context.Context, owner, repo string, pullRequestNumber int, state githubclt.StatusState, description, context string) error {
	c.logger.Info("simulated creating of github commit status, status has not been created on github",
		logfields.RepositoryOwner(owner),
		logfields.Repository(repo),
		logfields.PullRequest(pullRequestNumber),
		zap.String("github.status.state", string(state)),
		zap.String("github.status.description", description),
		zap.String("github.status.context", context),
	)

	return nil
}

func (c *DryGithubClient) ListPRs(ctx context.Context, owner, repo string) iter.Seq2[*githubclt.PR, error] {
	return c.clt.ListPRs(ctx, owner, repo)
}
