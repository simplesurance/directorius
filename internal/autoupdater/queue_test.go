package autoupdater

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"github.com/simplesurance/directorius/internal/autoupdater/mocks"
	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/retry"
)

func TestUpdatePR_DoesNotCallBaseBranchUpdateIfPRIsNotApproved(t *testing.T) {
	t.Cleanup(zap.ReplaceGlobals(zaptest.NewLogger(t).Named(t.Name())))

	mockctrl := gomock.NewController(t)
	ghClient := mocks.NewMockGithubClient(mockctrl)

	mockSuccessfulGithubRemoveLabelQueueHeadCall(ghClient, 1).Times(1)

	bb, err := NewBaseBranch(repoOwner, repo, "main")
	require.NoError(t, err)
	q := newQueue(bb, zap.L(), ghClient, retry.NewRetryer(), nil, "first")
	t.Cleanup(q.Stop)

	pr, err := NewPullRequest(1, "testbr", "fho", "test pr", "")
	require.NoError(t, err)

	_, added := q.active.InsertIfNotExist(pr.Number, pr)
	require.True(t, added)

	mockReadyForMergeStatus(
		ghClient, pr.Number,
		githubclt.ReviewDecisionChangesRequested, githubclt.CIStatusPending,
	).AnyTimes()
	ghClient.EXPECT().UpdateBranch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	q.updatePR(context.Background(), pr, TaskNone)
}

func TestUpdatePRWithBaseReturnsChangedWhenScheduled(t *testing.T) {
	t.Cleanup(zap.ReplaceGlobals(zaptest.NewLogger(t).Named(t.Name())))

	mockctrl := gomock.NewController(t)
	ghClient := mocks.NewMockGithubClient(mockctrl)

	var updateBranchCalls int32
	ghClient.
		EXPECT().
		UpdateBranch(gomock.Any(), gomock.Eq(repoOwner), gomock.Eq(repo), gomock.Any()).
		DoAndReturn(func(context.Context, string, string, int) (*githubclt.UpdateBranchResult, error) {
			if updateBranchCalls == 0 {
				updateBranchCalls++
				return &githubclt.UpdateBranchResult{HeadCommitID: headCommitID, Changed: true, Scheduled: true}, nil
			}
			updateBranchCalls++
			return &githubclt.UpdateBranchResult{HeadCommitID: headCommitID, Changed: false, Scheduled: false}, nil
		}).
		Times(2)

	bb, err := NewBaseBranch(repoOwner, repo, "main")
	require.NoError(t, err)
	q := newQueue(bb, zap.L(), ghClient, retry.NewRetryer(), nil, "first")

	pr, err := NewPullRequest(1, "pr_branch", "", "", "")
	require.NoError(t, err)
	changed, headCommit, err := q.updatePRWithBase(context.Background(), pr, zap.L(), nil)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, headCommitID, headCommit)
	q.Stop()
}

func TestActiveQueueOrder(t *testing.T) {
	t.Cleanup(zap.ReplaceGlobals(zaptest.NewLogger(t).Named(t.Name())))

	mockctrl := gomock.NewController(t)
	ghClient := mocks.NewMockGithubClient(mockctrl)

	bb, err := NewBaseBranch(repoOwner, repo, "main")
	require.NoError(t, err)
	q := newQueue(bb, zap.L(), ghClient, retry.NewRetryer(), &CI{}, "first")
	t.Cleanup(q.Stop)

	mockSuccessfulGithubRemoveLabelQueueHeadCall(ghClient, 1).AnyTimes()
	mockSuccessfulGithubAddLabelQueueHeadCall(ghClient, 1).AnyTimes()

	mockReadyForMergeStatus(
		ghClient, 1,
		githubclt.ReviewDecisionApproved, githubclt.CIStatusPending,
	).AnyTimes()
	mockSuccessfulGithubUpdateBranchCallAnyPR(ghClient, false).AnyTimes()

	// must always stay the first in the queue
	first, err := NewPullRequest(1, "testbr", "fho", "test pr", "")
	require.NoError(t, err)
	require.NoError(t, q.Enqueue(first))
	require.True(t, q.isFirstActive(first))

	prHighPrio, err := NewPullRequest(100, "testbr", "fho", "test pr", "")
	prHighPrio.EnqueuedAt = prHighPrio.EnqueuedAt.Add(24 * time.Hour)
	prHighPrio.Priority = 2
	prHighPrio.SuspendCount.Store(3)
	require.NoError(t, q.Enqueue(prHighPrio))
	require.NoError(t, err)
	require.True(t, q.isFirstActive(first))
	activePrs, _ := q.asSlices()
	require.Equal(t, prHighPrio, activePrs[1])

	// should become third in the queue because of it's priority
	prNegativePrio, err := NewPullRequest(3, "testbr", "fho", "test pr", "")
	require.NoError(t, err)
	prNegativePrio.Priority = -1
	require.NoError(t, q.Enqueue(prNegativePrio))
	require.True(t, q.isFirstActive(first))
	activePrs, _ = q.asSlices()
	require.Equal(t, prHighPrio, activePrs[1])
	require.Equal(t, prNegativePrio, activePrs[2])

	// should swap places with prNegativePrio because it has a higher priority
	prNeutralPrio, err := NewPullRequest(5, "testbr", "fho", "test pr", "")
	require.NoError(t, err)
	prNeutralPrio.Priority = 0
	require.NoError(t, q.Enqueue(prNeutralPrio))
	require.True(t, q.isFirstActive(first))
	activePrs, _ = q.asSlices()
	require.Equal(t, prHighPrio, activePrs[1])
	require.Equal(t, prNeutralPrio, activePrs[2])
	require.Equal(t, prNegativePrio, activePrs[3])

	// should swap places with prHighestPrio because it has an older EnqueuedAt timestamp
	prNeutralOld, err := NewPullRequest(6, "testbr", "fho", "test pr", "")
	require.NoError(t, err)
	prNeutralOld.Priority = 0
	prNeutralOld.EnqueuedAt = prNeutralOld.EnqueuedAt.Add(-24 * time.Hour)
	require.NoError(t, q.Enqueue(prNeutralOld))
	require.True(t, q.isFirstActive(first))
	activePrs, _ = q.asSlices()
	require.Equal(t, prHighPrio, activePrs[1])
	require.Equal(t, prNeutralOld, activePrs[2])
	require.Equal(t, prNeutralPrio, activePrs[3])
	require.Equal(t, prNegativePrio, activePrs[4])

	// should swap places with prHighPrio because it's SuspendCount is lower
	prHighPrio1S, err := NewPullRequest(200, "testbr", "fho", "test pr", "")
	prHighPrio1S.EnqueuedAt = prHighPrio.EnqueuedAt.Add(24 * time.Hour)
	prHighPrio1S.Priority = 2
	prHighPrio1S.SuspendCount.Store(1)
	require.NoError(t, q.Enqueue(prHighPrio1S))
	require.NoError(t, err)
	activePrs, _ = q.asSlices()
	require.Equal(t, prHighPrio1S, activePrs[1])
	require.Equal(t, prHighPrio, activePrs[2])
	require.Equal(t, prNeutralOld, activePrs[3])
	require.Equal(t, prNeutralPrio, activePrs[4])
	require.Equal(t, prNegativePrio, activePrs[5])
}
