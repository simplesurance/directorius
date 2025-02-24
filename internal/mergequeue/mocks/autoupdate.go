// Code generated by MockGen. DO NOT EDIT.
// Source: internal/mergequeue/coordinator.go
//
// Generated by this command:
//
//	mockgen -package mocks -source internal/mergequeue/coordinator.go -destination internal/mergequeue/mocks/autoupdate.go
//

// Package mocks is a generated GoMock package.
package mocks

import (
	context "context"
	iter "iter"
	reflect "reflect"

	githubclt "github.com/simplesurance/directorius/internal/githubclt"
	gomock "go.uber.org/mock/gomock"
)

// MockGithubClient is a mock of GithubClient interface.
type MockGithubClient struct {
	ctrl     *gomock.Controller
	recorder *MockGithubClientMockRecorder
	isgomock struct{}
}

// MockGithubClientMockRecorder is the mock recorder for MockGithubClient.
type MockGithubClientMockRecorder struct {
	mock *MockGithubClient
}

// NewMockGithubClient creates a new mock instance.
func NewMockGithubClient(ctrl *gomock.Controller) *MockGithubClient {
	mock := &MockGithubClient{ctrl: ctrl}
	mock.recorder = &MockGithubClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockGithubClient) EXPECT() *MockGithubClientMockRecorder {
	return m.recorder
}

// AddLabel mocks base method.
func (m *MockGithubClient) AddLabel(ctx context.Context, owner, repo string, pullRequestOrIssueNumber int, label string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "AddLabel", ctx, owner, repo, pullRequestOrIssueNumber, label)
	ret0, _ := ret[0].(error)
	return ret0
}

// AddLabel indicates an expected call of AddLabel.
func (mr *MockGithubClientMockRecorder) AddLabel(ctx, owner, repo, pullRequestOrIssueNumber, label any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "AddLabel", reflect.TypeOf((*MockGithubClient)(nil).AddLabel), ctx, owner, repo, pullRequestOrIssueNumber, label)
}

// CreateCommitStatus mocks base method.
func (m *MockGithubClient) CreateCommitStatus(ctx context.Context, owner, repo, commit string, state githubclt.StatusState, description, context string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateCommitStatus", ctx, owner, repo, commit, state, description, context)
	ret0, _ := ret[0].(error)
	return ret0
}

// CreateCommitStatus indicates an expected call of CreateCommitStatus.
func (mr *MockGithubClientMockRecorder) CreateCommitStatus(ctx, owner, repo, commit, state, description, context any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateCommitStatus", reflect.TypeOf((*MockGithubClient)(nil).CreateCommitStatus), ctx, owner, repo, commit, state, description, context)
}

// CreateHeadCommitStatus mocks base method.
func (m *MockGithubClient) CreateHeadCommitStatus(ctx context.Context, owner, repo string, pullRequestNumber int, state githubclt.StatusState, description, context string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateHeadCommitStatus", ctx, owner, repo, pullRequestNumber, state, description, context)
	ret0, _ := ret[0].(error)
	return ret0
}

// CreateHeadCommitStatus indicates an expected call of CreateHeadCommitStatus.
func (mr *MockGithubClientMockRecorder) CreateHeadCommitStatus(ctx, owner, repo, pullRequestNumber, state, description, context any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateHeadCommitStatus", reflect.TypeOf((*MockGithubClient)(nil).CreateHeadCommitStatus), ctx, owner, repo, pullRequestNumber, state, description, context)
}

// CreateIssueComment mocks base method.
func (m *MockGithubClient) CreateIssueComment(ctx context.Context, owner, repo string, issueOrPRNr int, comment string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateIssueComment", ctx, owner, repo, issueOrPRNr, comment)
	ret0, _ := ret[0].(error)
	return ret0
}

// CreateIssueComment indicates an expected call of CreateIssueComment.
func (mr *MockGithubClientMockRecorder) CreateIssueComment(ctx, owner, repo, issueOrPRNr, comment any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateIssueComment", reflect.TypeOf((*MockGithubClient)(nil).CreateIssueComment), ctx, owner, repo, issueOrPRNr, comment)
}

// ListPRs mocks base method.
func (m *MockGithubClient) ListPRs(ctx context.Context, owner, repo string) iter.Seq2[*githubclt.PR, error] {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListPRs", ctx, owner, repo)
	ret0, _ := ret[0].(iter.Seq2[*githubclt.PR, error])
	return ret0
}

// ListPRs indicates an expected call of ListPRs.
func (mr *MockGithubClientMockRecorder) ListPRs(ctx, owner, repo any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListPRs", reflect.TypeOf((*MockGithubClient)(nil).ListPRs), ctx, owner, repo)
}

// ReadyForMerge mocks base method.
func (m *MockGithubClient) ReadyForMerge(ctx context.Context, owner, repo string, prNumber int) (*githubclt.ReadyForMergeStatus, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ReadyForMerge", ctx, owner, repo, prNumber)
	ret0, _ := ret[0].(*githubclt.ReadyForMergeStatus)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ReadyForMerge indicates an expected call of ReadyForMerge.
func (mr *MockGithubClientMockRecorder) ReadyForMerge(ctx, owner, repo, prNumber any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ReadyForMerge", reflect.TypeOf((*MockGithubClient)(nil).ReadyForMerge), ctx, owner, repo, prNumber)
}

// RemoveLabel mocks base method.
func (m *MockGithubClient) RemoveLabel(ctx context.Context, owner, repo string, pullRequestOrIssueNumber int, label string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "RemoveLabel", ctx, owner, repo, pullRequestOrIssueNumber, label)
	ret0, _ := ret[0].(error)
	return ret0
}

// RemoveLabel indicates an expected call of RemoveLabel.
func (mr *MockGithubClientMockRecorder) RemoveLabel(ctx, owner, repo, pullRequestOrIssueNumber, label any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "RemoveLabel", reflect.TypeOf((*MockGithubClient)(nil).RemoveLabel), ctx, owner, repo, pullRequestOrIssueNumber, label)
}

// UpdateBranch mocks base method.
func (m *MockGithubClient) UpdateBranch(ctx context.Context, owner, repo string, pullRequestNumber int) (*githubclt.UpdateBranchResult, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateBranch", ctx, owner, repo, pullRequestNumber)
	ret0, _ := ret[0].(*githubclt.UpdateBranchResult)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// UpdateBranch indicates an expected call of UpdateBranch.
func (mr *MockGithubClientMockRecorder) UpdateBranch(ctx, owner, repo, pullRequestNumber any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateBranch", reflect.TypeOf((*MockGithubClient)(nil).UpdateBranch), ctx, owner, repo, pullRequestNumber)
}
