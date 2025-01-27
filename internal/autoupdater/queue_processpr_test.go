package autoupdater

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"

	"github.com/simplesurance/directorius/internal/autoupdater/mocks"
	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/jenkins"
	"github.com/simplesurance/directorius/internal/retry"
)

type evalPRTest struct {
	Q     *queue
	L     *zap.Logger
	PR    *PullRequest
	GhClt *mocks.MockGithubClient
}

func initEvalPRActionTest(t *testing.T) *evalPRTest {
	l := forwardLogsToTestLogger(t)

	mockctrl := gomock.NewController(t, gomock.WithOverridableExpectations())
	ghClient := mocks.NewMockGithubClient(mockctrl)
	ciClient := mocks.NewMockCIClient(mockctrl)

	mockSuccessfulGithubUpdateBranchCallAnyPR(ghClient, false).AnyTimes()
	mockReadyForMergeStatus(
		ghClient,
		prNR,
		githubclt.ReviewDecisionApproved, githubclt.CIStatusSuccess,
	).AnyTimes()

	bb, err := NewBaseBranch(repoOwner, repo, "main")
	require.NoError(t, err)
	ci := &CI{Client: ciClient, logger: l}
	q := newQueue(bb, l, ghClient, retry.NewRetryer(), ci, queueHeadLabel)
	t.Cleanup(q.Stop)

	pr, err := NewPullRequest(prNR, prBranch, "me", "test pr", "")
	require.NoError(t, err)

	return &evalPRTest{
		Q:     q,
		L:     l,
		PR:    pr,
		GhClt: ghClient,
	}
}

func successfulCIJobStatuses() []*githubclt.CIJobStatus {
	return []*githubclt.CIJobStatus{
		{
			Name:     "build",
			Required: true,
			Status:   githubclt.CIStatusSuccess,
			JobURL:   "http://localhost/job/build/1",
		},
		{
			Name:     "unittest",
			Required: false,
			Status:   githubclt.CIStatusSuccess,
			JobURL:   "http://localhost/job/unittest/1",
		},
		{
			Name:     "check",
			Required: false,
			Status:   githubclt.CIStatusFailure,
			JobURL:   "http://localhost/job/check/1",
		},
	}
}

func jenkinsJobURL(jobName, buildNr string) string {
	return "http://localhost.test/job/" + jobName + "/" + buildNr
}

const failedCIJobName = "integrationtest"

func failedCIJobStatuses() []*githubclt.CIJobStatus {
	return append(
		successfulCIJobStatuses(),
		&githubclt.CIJobStatus{
			Name:     failedCIJobName,
			Required: true,
			Status:   githubclt.CIStatusFailure,
			JobURL:   jenkinsJobURL(failedCIJobName, "1"),
		})
}

func TestEvalPRAction_SuspendActionOnNotTriggeredFailedCIStatus(t *testing.T) {
	prt := initEvalPRActionTest(t)
	prt.GhClt.EXPECT().
		ReadyForMerge(gomock.Any(), gomock.Eq(repoOwner), gomock.Eq(repo), gomock.Eq(prNR)).
		DoAndReturn(func(context.Context, string, string, int) (*githubclt.ReadyForMergeStatus, error) {
			return &githubclt.ReadyForMergeStatus{
				ReviewDecision: githubclt.ReviewDecisionApproved,
				CIStatus:       githubclt.CIStatusFailure,
				Statuses:       failedCIJobStatuses(),
			}, nil
		}).Times(1)

	jb, err := jenkins.ParseBuildURL(jenkinsJobURL("check", "1"))
	require.NoError(t, err)
	prt.PR.SetLastStartedCIBuilds(map[string]*jenkins.Build{"check": jb})

	reqActions, err := prt.Q.evalPRAction(context.Background(), prt.L, prt.PR)
	require.NoError(t, err)
	require.NotNil(t, reqActions)
	require.Len(t, reqActions.Actions, 1)
	require.Equal(t, ActionSuspend, reqActions.Actions[0])
}

func TestEvalPRAction_PRNotSuspendforObsoleteCIBuildFailures(t *testing.T) {
	const ciJobName = "check"

	prt := initEvalPRActionTest(t)
	prt.PR.lastStartedCIBuilds = map[string]*jenkins.Build{
		ciJobName: {
			JobName: ciJobName,
			Number:  2,
		},
	}

	prt.GhClt.
		EXPECT().
		ReadyForMerge(gomock.Any(), gomock.Eq(repoOwner), gomock.Eq(repo), gomock.Eq(prNR)).
		DoAndReturn(func(context.Context, string, string, int) (*githubclt.ReadyForMergeStatus, error) {
			return &githubclt.ReadyForMergeStatus{
				ReviewDecision: githubclt.ReviewDecisionApproved,
				CIStatus:       githubclt.CIStatusFailure,
				Statuses: append(successfulCIJobStatuses(),
					&githubclt.CIJobStatus{
						Name:     ciJobName,
						Status:   githubclt.CIStatusFailure,
						Required: true,
						JobURL:   jenkinsJobURL(ciJobName, "1"),
					}),
			}, nil
		}).Times(1)
	reqActions, err := prt.Q.evalPRAction(context.Background(), prt.L, prt.PR)
	require.NoError(t, err)
	require.NotNil(t, reqActions)
	require.Len(t, reqActions.Actions, 1)
	require.Equal(t, ActionNone, reqActions.Actions[0])
}

func TestEvalPRAction_PendingCIJobs(t *testing.T) {
	prt := initEvalPRActionTest(t)

	prt.GhClt.
		EXPECT().
		ReadyForMerge(gomock.Any(), gomock.Eq(repoOwner), gomock.Eq(repo), gomock.Eq(prNR)).
		DoAndReturn(func(context.Context, string, string, int) (*githubclt.ReadyForMergeStatus, error) {
			return &githubclt.ReadyForMergeStatus{
				ReviewDecision: githubclt.ReviewDecisionApproved,
				CIStatus:       githubclt.CIStatusPending,
				Statuses: []*githubclt.CIJobStatus{
					{
						Name:     "build",
						Required: true,
						Status:   githubclt.CIStatusPending,
						JobURL:   "http://localhost/job/build/1",
					},
					{
						Name:     "unittest",
						Required: true,
						Status:   githubclt.CIStatusSuccess,
						JobURL:   "http://localhost/job/unittest/1",
					},
					{
						Name:     "check",
						Required: true,
						Status:   githubclt.CIStatusPending,
						JobURL:   "http://localhost/job/check/1",
					},
				},
			}, nil
		}).Times(1)
	reqActions, err := prt.Q.evalPRAction(context.Background(), prt.L, prt.PR)
	require.NoError(t, err)
	require.NotNil(t, reqActions)
	require.Len(t, reqActions.Actions, 2)
	assert.Contains(t, reqActions.Actions, ActionAddFirstInQueueGithubLabel)
	assert.Contains(t, reqActions.Actions, ActionCreateSuccessfulGithubStatus)
}
