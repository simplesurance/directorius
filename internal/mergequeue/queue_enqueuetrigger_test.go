package mergequeue

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/mergequeue/mocks"
	github_prov "github.com/simplesurance/directorius/internal/provider/github"
	"github.com/simplesurance/directorius/internal/set"
)

const triggerLabel = "queue-add"

// approvedStatus returns the ready for merge status of an open, approved pull
// request with successful CI jobs.
func approvedStatus(autoMergeEnabled bool, labels ...string) *githubclt.ReadyForMergeStatus {
	return &githubclt.ReadyForMergeStatus{
		ReviewDecision:   githubclt.ReviewDecisionApproved,
		CIStatus:         githubclt.CIStatusSuccess,
		Commit:           dryGitHubClientHeadCommitID,
		AutoMergeEnabled: autoMergeEnabled,
		Labels:           labels,
	}
}

// mockReadyForMergeStatusFunc replaces the ReadyForMerge mock, the returned
// status is created by fn for the passed pull request number.
// The mock controller must have been created with overridable expectations.
func mockReadyForMergeStatusFunc(clt *mocks.MockGithubClient, fn func(prNumber int) *githubclt.ReadyForMergeStatus) {
	clt.EXPECT().
		ReadyForMerge(gomock.Any(), gomock.Eq(repoOwner), gomock.Eq(repo), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, prNumber int) (*githubclt.ReadyForMergeStatus, error) {
			return fn(prNumber), nil
		}).
		AnyTimes()
}

func mockReadyForMergeStatusConst(clt *mocks.MockGithubClient, status *githubclt.ReadyForMergeStatus) {
	mockReadyForMergeStatusFunc(clt, func(int) *githubclt.ReadyForMergeStatus {
		s := *status
		return &s
	})
}

// mockNoUpdateBranchCalls fails the test when UpdateBranch is called.
func mockNoUpdateBranchCalls(clt *mocks.MockGithubClient) {
	clt.EXPECT().
		UpdateBranch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Times(0)
}

func TestEnqueueTrigger_IsMet(t *testing.T) {
	tcs := []struct {
		name             string
		trigger          enqueueTrigger
		autoMergeEnabled bool
		labels           []string
		want             bool
	}{
		{
			name:    "no trigger configured, condition is not verified",
			trigger: enqueueTrigger{},
			want:    true,
		},
		{
			name:    "no trigger configured, empty label set",
			trigger: enqueueTrigger{Labels: set.Set[string]{}},
			want:    true,
		},
		{
			name:             "auto-merge trigger, auto-merge enabled",
			trigger:          enqueueTrigger{OnAutomerge: true},
			autoMergeEnabled: true,
			want:             true,
		},
		{
			name:    "auto-merge trigger, auto-merge disabled",
			trigger: enqueueTrigger{OnAutomerge: true},
			labels:  []string{triggerLabel},
			want:    false,
		},
		{
			name:    "label trigger, label set",
			trigger: enqueueTrigger{Labels: set.From([]string{triggerLabel})},
			labels:  []string{"bug", triggerLabel},
			want:    true,
		},
		{
			name:    "label trigger, label missing",
			trigger: enqueueTrigger{Labels: set.From([]string{triggerLabel})},
			labels:  []string{"bug"},
			want:    false,
		},
		{
			name:             "label trigger, auto-merge enabled is irrelevant",
			trigger:          enqueueTrigger{Labels: set.From([]string{triggerLabel})},
			autoMergeEnabled: true,
			want:             false,
		},
		{
			name:             "both triggers, only auto-merge",
			trigger:          enqueueTrigger{OnAutomerge: true, Labels: set.From([]string{triggerLabel})},
			autoMergeEnabled: true,
			want:             true,
		},
		{
			name:    "both triggers, only label",
			trigger: enqueueTrigger{OnAutomerge: true, Labels: set.From([]string{triggerLabel})},
			labels:  []string{triggerLabel},
			want:    true,
		},
		{
			name:    "both triggers, none met",
			trigger: enqueueTrigger{OnAutomerge: true, Labels: set.From([]string{triggerLabel})},
			labels:  []string{"bug"},
			want:    false,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.trigger.isMet(tc.autoMergeEnabled, tc.labels))
		})
	}
}

type triggerTestCase struct {
	name        string
	onAutomerge bool
	labels      []string
	status      *githubclt.ReadyForMergeStatus
}

func TestEvalPRAction_DequeuesPRThatDoesNotMeetTriggerAnymore(t *testing.T) {
	tcs := []triggerTestCase{
		{
			name:        "auto-merge disabled",
			onAutomerge: true,
			status:      approvedStatus(false),
		},
		{
			name:        "auto-merge disabled, unrelated label",
			onAutomerge: true,
			status:      approvedStatus(false, "bug"),
		},
		{
			name:   "trigger label removed",
			labels: []string{triggerLabel},
			status: approvedStatus(false, "bug"),
		},
		{
			name:   "trigger label removed, auto-merge is enabled but not a trigger",
			labels: []string{triggerLabel},
			status: approvedStatus(true),
		},
		{
			name:        "both triggers configured, none met",
			onAutomerge: true,
			labels:      []string{triggerLabel},
			status:      approvedStatus(false, "bug"),
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			prt := initEvalPRActionTest(t, withEnqueueTrigger(tc.onAutomerge, set.From(tc.labels)))
			mockReadyForMergeStatusConst(prt.GhClt, tc.status)
			// the branch must not be updated, it would trigger CI
			mockNoUpdateBranchCalls(prt.GhClt)

			reqActions, err := prt.Q.evalPRAction(context.Background(), prt.L, prt.PR)
			require.NoError(t, err)
			require.NotNil(t, reqActions)

			assert.Equal(t, []Action{ActionDequeue}, reqActions.Actions)
			assert.Equal(t, reasonTriggerNotMet, reqActions.Reason)
			assert.True(t, reqActions.ResetStatusOnDequeue, "github status of an open pr must be reset")
		})
	}
}

func TestEvalPRAction_DoesNotDequeuePRThatMeetsTrigger(t *testing.T) {
	tcs := []triggerTestCase{
		{
			name:        "auto-merge enabled",
			onAutomerge: true,
			status:      approvedStatus(true),
		},
		{
			name:   "trigger label set",
			labels: []string{triggerLabel},
			status: approvedStatus(false, "bug", triggerLabel),
		},
		{
			name:        "both triggers configured, only auto-merge",
			onAutomerge: true,
			labels:      []string{triggerLabel},
			status:      approvedStatus(true),
		},
		{
			name:        "both triggers configured, only label",
			onAutomerge: true,
			labels:      []string{triggerLabel},
			status:      approvedStatus(false, triggerLabel),
		},
		{
			name:   "no trigger configured",
			status: approvedStatus(false),
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			prt := initEvalPRActionTest(t, withEnqueueTrigger(tc.onAutomerge, set.From(tc.labels)))
			mockReadyForMergeStatusConst(prt.GhClt, tc.status)

			reqActions, err := prt.Q.evalPRAction(context.Background(), prt.L, prt.PR)
			require.NoError(t, err)
			require.NotNil(t, reqActions)

			assert.NotContains(t, reqActions.Actions, ActionDequeue)
			assert.Equal(t, []Action{ActionCreateSuccessfulGithubStatus, ActionNone}, reqActions.Actions)
		})
	}
}

func TestEvalPRAction_DequeuesClosedPR(t *testing.T) {
	tcs := []triggerTestCase{
		{
			name:        "trigger is met",
			onAutomerge: true,
			status:      approvedStatus(true),
		},
		{
			name:        "trigger is not met",
			onAutomerge: true,
			status:      approvedStatus(false),
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			prt := initEvalPRActionTest(t, withEnqueueTrigger(tc.onAutomerge, set.From(tc.labels)))
			tc.status.Closed = true
			mockReadyForMergeStatusConst(prt.GhClt, tc.status)
			mockNoUpdateBranchCalls(prt.GhClt)

			reqActions, err := prt.Q.evalPRAction(context.Background(), prt.L, prt.PR)
			require.NoError(t, err)
			require.NotNil(t, reqActions)

			assert.Equal(t, []Action{ActionDequeue}, reqActions.Actions)
			assert.Equal(t, reasonPullRequestClosed, reqActions.Reason)
			assert.False(t, reqActions.ResetStatusOnDequeue, "github status of a closed pr must not be reset")
		})
	}
}

func TestProcessPR_DequeuesFirstPRThatDoesNotMeetTriggerAnymore(t *testing.T) {
	var emptied atomic.Int32

	prt := initEvalPRActionTest(t,
		withEnqueueTrigger(true, nil),
		withOnEmpty(func() { emptied.Add(1) }),
	)
	mockReadyForMergeStatusConst(prt.GhClt, approvedStatus(false))
	mockNoUpdateBranchCalls(prt.GhClt)
	// unexpected AddLabel and CreateCommitStatus calls fail the test.
	mockSuccessfulGithubRemoveLabelQueueHeadCall(prt.GhClt, prNR).Times(1)
	mockCreateHeadCommitStatusPendingPRNr(prt.GhClt, prNR).Times(1)

	require.NoError(t, prt.Q.Enqueue(prt.PR))

	require.Eventually(t,
		func() bool { return emptied.Load() == 1 },
		condWaitTimeout, condCheckInterval,
		"queue was not reported as empty after the pr was dequeued",
	)
	assert.True(t, prt.Q.IsEmpty())
}

func TestProcessPR_DequeuesClosedFirstPRWithoutResettingStatus(t *testing.T) {
	var emptied atomic.Int32

	prt := initEvalPRActionTest(t,
		withEnqueueTrigger(true, nil),
		withOnEmpty(func() { emptied.Add(1) }),
	)
	status := approvedStatus(true)
	status.Closed = true
	mockReadyForMergeStatusConst(prt.GhClt, status)
	mockNoUpdateBranchCalls(prt.GhClt)
	// unexpected CreateHeadCommitStatus calls fail the test.
	mockSuccessfulGithubRemoveLabelQueueHeadCall(prt.GhClt, prNR).Times(1)

	require.NoError(t, prt.Q.Enqueue(prt.PR))

	require.Eventually(t,
		func() bool { return emptied.Load() == 1 },
		condWaitTimeout, condCheckInterval,
		"queue was not reported as empty after the pr was dequeued",
	)
	assert.True(t, prt.Q.IsEmpty())
}

func TestProcessPR_ProcessesNextPRAfterFirstDoesNotMeetTriggerAnymore(t *testing.T) {
	const nextPRNr = prNR + 1

	var emptied atomic.Int32
	var updateBranchCallsFirstPR, updateBranchCallsNextPR atomic.Int32

	prt := initEvalPRActionTest(t,
		withEnqueueTrigger(true, nil),
		withOnEmpty(func() { emptied.Add(1) }),
	)
	mockReadyForMergeStatusFunc(prt.GhClt, func(prNumber int) *githubclt.ReadyForMergeStatus {
		// the auto-merge disabled event for the first pr was lost
		return approvedStatus(prNumber != prNR)
	})
	prt.GhClt.EXPECT().
		UpdateBranch(gomock.Any(), gomock.Eq(repoOwner), gomock.Eq(repo), gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _ string, prNumber int) (*githubclt.UpdateBranchResult, error) {
			if prNumber == prNR {
				updateBranchCallsFirstPR.Add(1)
			} else {
				updateBranchCallsNextPR.Add(1)
			}
			return &githubclt.UpdateBranchResult{HeadCommitID: dryGitHubClientHeadCommitID}, nil
		}).
		AnyTimes()
	mockSuccessfulGithubRemoveLabelQueueHeadCall(prt.GhClt, prNR).Times(1)
	mockCreateHeadCommitStatusPendingPRNr(prt.GhClt, prNR).Times(1)
	mockSuccessfulGithubAddLabelQueueHeadCall(prt.GhClt, nextPRNr).AnyTimes()
	mockCreateCommitStatusSuccessful(prt.GhClt).AnyTimes()

	nextPR, err := NewPullRequest(nextPRNr, "next_branch", "me", "next pr", "")
	require.NoError(t, err)

	require.NoError(t, prt.Q.Enqueue(prt.PR))
	require.NoError(t, prt.Q.Enqueue(nextPR))

	require.Eventually(t,
		func() bool {
			first := prt.Q.FirstActive()
			return first != nil && first.Number == nextPRNr && updateBranchCallsNextPR.Load() > 0
		},
		condWaitTimeout, condCheckInterval,
		"the pr after the dequeued one was not processed",
	)

	assert.Equal(t, 1, prt.Q.activeLen())
	assert.Zero(t, updateBranchCallsFirstPR.Load(), "branch of the dequeued pr was updated")
	assert.Zero(t, emptied.Load(), "queue is not empty")
}

func suspendPR(q *queue, pr *PullRequest) {
	q.lock.Lock()
	defer q.lock.Unlock()
	q.suspended[pr.Number] = pr
}

func TestResumeIfPRMergeStatusPositive_DequeuesPRThatDoesNotMeetTriggerAnymore(t *testing.T) {
	var emptied atomic.Int32

	// A suspended PR had its label already removed and its status reset when
	// it was suspended, calls for it would fail the test.
	prt := initEvalPRActionTest(t,
		withEnqueueTrigger(false, set.From([]string{triggerLabel})),
		withOnEmpty(func() { emptied.Add(1) }),
	)
	mockReadyForMergeStatusConst(prt.GhClt, approvedStatus(false, "bug"))
	suspendPR(prt.Q, prt.PR)

	err := prt.Q.resumeIfPRMergeStatusPositive(context.Background(), prt.L, prt.PR)
	require.NoError(t, err)

	assert.Zero(t, prt.Q.suspendedLen(), "pr is still in suspend queue")
	assert.Zero(t, prt.Q.activeLen(), "pr was moved to active queue")
	assert.Equal(t, int32(1), emptied.Load())
}

func TestResumeIfPRMergeStatusPositive_DequeuesClosedPR(t *testing.T) {
	var emptied atomic.Int32

	prt := initEvalPRActionTest(t,
		withEnqueueTrigger(true, nil),
		withOnEmpty(func() { emptied.Add(1) }),
	)
	status := approvedStatus(true)
	status.Closed = true
	mockReadyForMergeStatusConst(prt.GhClt, status)
	suspendPR(prt.Q, prt.PR)

	err := prt.Q.resumeIfPRMergeStatusPositive(context.Background(), prt.L, prt.PR)
	require.NoError(t, err)

	assert.Zero(t, prt.Q.suspendedLen(), "pr is still in suspend queue")
	assert.Zero(t, prt.Q.activeLen(), "pr was moved to active queue")
	assert.Equal(t, int32(1), emptied.Load())
}

func TestResumeIfPRMergeStatusPositive_KeepsPRThatMeetsTrigger(t *testing.T) {
	var emptied atomic.Int32

	prt := initEvalPRActionTest(t,
		withEnqueueTrigger(true, nil),
		withOnEmpty(func() { emptied.Add(1) }),
	)
	status := approvedStatus(true)
	status.ReviewDecision = githubclt.ReviewDecisionReviewRequired
	mockReadyForMergeStatusConst(prt.GhClt, status)
	suspendPR(prt.Q, prt.PR)

	err := prt.Q.resumeIfPRMergeStatusPositive(context.Background(), prt.L, prt.PR)
	require.NoError(t, err)

	assert.Equal(t, 1, prt.Q.suspendedLen(), "pr was removed from suspend queue")
	assert.Zero(t, emptied.Load())
}

// TestMissedAutoMergeDisabledEventDequeuesPRAndRemovesQueue verifies that a
// queued PR for that GitHub never delivered an auto_merge_disabled event, does
// not block the queue forever.
// When the base branch changes, the PR is removed from the queue, the queue head
// label is removed and the empty queue is removed from the coordinator.
func TestMissedAutoMergeDisabledEventDequeuesPRAndRemovesQueue(t *testing.T) {
	const prNumber = 1
	const prBranch = "pr_branch"

	evChan := make(chan *github_prov.Event, 1)
	defer close(evChan)

	mockctrl := gomock.NewController(t)
	ghClient := mocks.NewMockGithubClient(mockctrl)
	ciClient := mocks.NewMockJenkinsClient(mockctrl)
	forwardLogsToTestLogger(t)

	// Called once, when the PR is enqueued. When the PR is processed
	// after it is ineligible, the branch must not be updated.
	mockSuccessfulGithubUpdateBranchCall(ghClient, prNumber, true).Times(1)
	status := mockReadyForMergeStatus(
		ghClient, prNumber,
		githubclt.ReviewDecisionApproved, githubclt.CIStatusExpected,
	)
	status.AnyTimes()
	mockSuccessfulGithubRemoveLabelQueueHeadCall(ghClient, prNumber).Times(1)
	mockCreateHeadCommitStatusPendingPRNr(ghClient, prNumber).Times(1)

	autoupdater := newAutoupdater(
		ghClient,
		ciClient,
		evChan,
		[]Repository{{OwnerLogin: repoOwner, RepositoryName: repo}},
		true,
		nil,
	)
	autoupdater.Start()
	t.Cleanup(autoupdater.Stop)

	baseBranch, err := NewBaseBranch(repoOwner, repo, "main")
	require.NoError(t, err)

	evChan <- &github_prov.Event{Event: newPullRequestAutomergeEnabledEvent(prNumber, prBranch, baseBranch.Branch)}
	waitForProcessedEventCnt(t, autoupdater, 1)

	q := autoupdater.getQueue(&baseBranch.BranchID)
	require.NotNil(t, q)
	require.Equal(t, 1, q.activeLen())
	// wait until the PR was processed once, it is the first in the queue
	waitForQueueUpdateRunsGreaterThan(t, q, 0)

	// auto-merge is disabled on GitHub but the auto_merge_disabled event is lost
	status.mu.Lock()
	status.AutoMergeEnabled = false
	status.mu.Unlock()

	evChan <- &github_prov.Event{Event: newPushEvent(baseBranch.Branch)}
	waitForProcessedEventCnt(t, autoupdater, 2)

	require.Eventually(t,
		func() bool { return autoupdater.getQueue(&baseBranch.BranchID) == nil },
		condWaitTimeout, condCheckInterval,
		"empty queue was not removed after the pr was dequeued",
	)
}

// A webhook event can dequeue a PR while its status is retrieved from GitHub.
// The queue must handle it as no failure and report that it became empty.
// The PR is dequeued by the event, not by the queue itself, therefore onEmpty
// can only be called by the code under test.
func TestResumeIfPRMergeStatusPositive_PRDequeuedConcurrently(t *testing.T) {
	var emptied atomic.Int32

	prt := initEvalPRActionTest(t,
		withEnqueueTrigger(true, nil),
		withOnEmpty(func() { emptied.Add(1) }),
	)
	suspendPR(prt.Q, prt.PR)

	prt.GhClt.EXPECT().
		ReadyForMerge(gomock.Any(), gomock.Eq(repoOwner), gomock.Eq(repo), gomock.Eq(prNR)).
		DoAndReturn(func(context.Context, string, string, int) (*githubclt.ReadyForMergeStatus, error) {
			_, err := prt.Q.Dequeue(prNR, true)
			require.NoError(t, err)

			return approvedStatus(false), nil
		}).
		Times(1)

	err := prt.Q.resumeIfPRMergeStatusPositive(context.Background(), prt.L, prt.PR)
	require.ErrorIs(t, err, ErrNotFound)

	assert.Equal(t, int32(1), emptied.Load())
}

func TestProcessPR_PRDequeuedConcurrently(t *testing.T) {
	var emptied atomic.Int32

	prt := initEvalPRActionTest(t,
		withEnqueueTrigger(true, nil),
		withOnEmpty(func() { emptied.Add(1) }),
	)
	mockNoUpdateBranchCalls(prt.GhClt)

	// The label is removed and the status reset once, by the event. The
	// queue must not repeat it when it finds out that the PR is gone.
	mockSuccessfulGithubRemoveLabelQueueHeadCall(prt.GhClt, prNR).Times(1)
	mockCreateHeadCommitStatusPendingPRNr(prt.GhClt, prNR).Times(1)

	prt.GhClt.EXPECT().
		ReadyForMerge(gomock.Any(), gomock.Eq(repoOwner), gomock.Eq(repo), gomock.Eq(prNR)).
		DoAndReturn(func(context.Context, string, string, int) (*githubclt.ReadyForMergeStatus, error) {
			_, err := prt.Q.Dequeue(prNR, true)
			require.NoError(t, err)

			return approvedStatus(false), nil
		}).
		Times(1)

	// Insert without scheduling processPR, it is run directly.
	prt.Q.lock.Lock()
	_, added := prt.Q.active.InsertIfNotExist(prt.PR.Number, prt.PR)
	prt.Q.lock.Unlock()
	require.True(t, added)

	prt.Q.processPR(context.Background(), prt.PR, TaskNone)

	assert.True(t, prt.Q.IsEmpty())
	assert.Equal(t, int32(1), emptied.Load())
}
