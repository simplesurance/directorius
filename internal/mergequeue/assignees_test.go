package mergequeue

import (
	"testing"

	"github.com/google/go-github/v67/github"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/mergequeue/mocks"
	github_prov "github.com/simplesurance/directorius/internal/provider/github"
)

func ghUsers(logins ...string) []*github.User {
	result := make([]*github.User, 0, len(logins))
	for _, l := range logins {
		result = append(result, &github.User{Login: strPtr(l)})
	}
	return result
}

func TestAssigneesAreTrackedFromEvents(t *testing.T) {
	t.Cleanup(zap.ReplaceGlobals(zaptest.NewLogger(t).Named(t.Name())))

	evChan := make(chan *github_prov.Event, 1)
	defer close(evChan)

	mockctrl := gomock.NewController(t)
	ghClient := mocks.NewMockGithubClient(mockctrl)
	ciClient := mocks.NewMockJenkinsClient(mockctrl)

	prNumber := 1
	prBranch := "pr_branch"
	baseBranch := "main"
	triggerLabel := "queue-add"

	mockSuccessfulGithubUpdateBranchCall(ghClient, prNumber, false).AnyTimes()
	mockReadyForMergeStatus(
		ghClient, prNumber,
		githubclt.ReviewDecisionApproved, githubclt.CIStatusPending,
	).AnyTimes()
	mockSuccessfulGithubAddLabelQueueHeadCall(ghClient, prNumber).AnyTimes()
	mockCreateCommitStatusSuccessful(ghClient).AnyTimes()

	autoupdater := newAutoupdater(
		ghClient,
		ciClient,
		evChan,
		[]Repository{{OwnerLogin: repoOwner, RepositoryName: repo}},
		true,
		[]string{triggerLabel},
	)
	autoupdater.Start()
	t.Cleanup(autoupdater.Stop)

	labeled := newPullRequestLabeledEvent(prNumber, prBranch, baseBranch, triggerLabel)
	labeled.PullRequest.Assignees = ghUsers("alice")
	evChan <- &github_prov.Event{Event: labeled}
	waitForProcessedEventCnt(t, autoupdater, 1)

	q := autoupdater.getQueue(&BranchID{RepositoryOwner: repoOwner, Repository: repo, Branch: baseBranch})
	require.NotNil(t, q)
	pr := q.getPullRequest(prNumber)
	require.NotNil(t, pr)
	require.Equal(t, []string{"alice"}, pr.Assignees())

	assigned := newBasicPullRequestEvent(prNumber, prBranch, baseBranch)
	assigned.Action = strPtr("assigned")
	assigned.PullRequest.Assignees = ghUsers("alice", "bob")
	evChan <- &github_prov.Event{Event: assigned}
	waitForProcessedEventCnt(t, autoupdater, 2)

	require.Equal(t, []string{"alice", "bob"}, pr.Assignees())

	unassigned := newBasicPullRequestEvent(prNumber, prBranch, baseBranch)
	unassigned.Action = strPtr("unassigned")
	evChan <- &github_prov.Event{Event: unassigned}
	waitForProcessedEventCnt(t, autoupdater, 3)

	require.Empty(t, pr.Assignees())
}

func TestSetAssigneesCopiesSlice(t *testing.T) {
	pr, err := NewPullRequest(1, "br", "", "", "")
	require.NoError(t, err)
	require.Empty(t, pr.Assignees())

	in := []string{"alice"}
	pr.SetAssignees(in)
	in[0] = "mallory"
	require.Equal(t, []string{"alice"}, pr.Assignees())

	out := pr.Assignees()
	out[0] = "mallory"
	require.Equal(t, []string{"alice"}, pr.Assignees())
}
