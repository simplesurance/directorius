package autoupdater

import (
	"context"

	"go.uber.org/zap"

	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/logfields"
)

const headCommitID = "32d4ff96ea72412277bbfd22ff1bab3a5263b415"

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
	c.logger.Info("simulated updating of github branch, returning is uptodate")
	return &githubclt.UpdateBranchResult{HeadCommitID: headCommitID}, nil
}

func (c *DryGithubClient) ReadyForMerge(context.Context, string, string, int) (*githubclt.ReadyForMergeStatus, error) {
	c.logger.Info("simulated fetching ready for merge status, pr is approved, all checks successful")

	return &githubclt.ReadyForMergeStatus{
		ReviewDecision: githubclt.ReviewDecisionApproved,
		CIStatus:       githubclt.CIStatusSuccess,
		Commit:         headCommitID,
	}, nil
}

func (c *DryGithubClient) CreateIssueComment(context.Context, string, string, int, string) error {
	c.logger.Info("simulated creating of github issue comment, no comment created on github")
	return nil
}

func (c *DryGithubClient) ListPullRequests(ctx context.Context, owner, repo, state, sort, sortDirection string) githubclt.PRIterator {
	return c.clt.ListPullRequests(ctx, owner, repo, state, sort, sortDirection)
}

func (*DryGithubClient) AddLabel(context.Context, string, string, int, string) error {
	return nil
}

func (*DryGithubClient) RemoveLabel(context.Context, string, string, int, string) error {
	return nil
}

func (c *DryGithubClient) CreateCommitStatus(_ context.Context, owner, repo, commit, state, description, context string) error {
	c.logger.Info("simulated creating of github commit status, status has not been created on github",
		logfields.RepositoryOwner(owner),
		logfields.Repository(repo),
		logfields.Commit(commit),
		zap.String("github.status.state", state),
		zap.String("github.status.description", description),
		zap.String("github.status.context", context),
	)

	return nil
}

func (c *DryGithubClient) CreateHeadCommitStatus(ctx context.Context, owner, repo string, pullRequestNumber int, state, description, context string) error {
	c.logger.Info("simulated creating of github commit status, status has not been created on github",
		logfields.RepositoryOwner(owner),
		logfields.Repository(repo),
		logfields.PullRequest(pullRequestNumber),
		zap.String("github.status.state", state),
		zap.String("github.status.description", description),
		zap.String("github.status.context", context),
	)

	return nil
}
