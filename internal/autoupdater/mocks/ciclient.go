// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/simplesurance/directorius/internal/autoupdater (interfaces: CIClient)
//
// Generated by this command:
//
//	mockgen -package mocks -destination internal/autoupdater/mocks/ciclient.go github.com/simplesurance/directorius/internal/autoupdater CIClient
//

// Package mocks is a generated GoMock package.
package mocks

import (
	context "context"
	reflect "reflect"

	jenkins "github.com/simplesurance/directorius/internal/jenkins"
	gomock "go.uber.org/mock/gomock"
)

// MockCIClient is a mock of CIClient interface.
type MockCIClient struct {
	ctrl     *gomock.Controller
	recorder *MockCIClientMockRecorder
	isgomock struct{}
}

// MockCIClientMockRecorder is the mock recorder for MockCIClient.
type MockCIClientMockRecorder struct {
	mock *MockCIClient
}

// NewMockCIClient creates a new mock instance.
func NewMockCIClient(ctrl *gomock.Controller) *MockCIClient {
	mock := &MockCIClient{ctrl: ctrl}
	mock.recorder = &MockCIClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockCIClient) EXPECT() *MockCIClientMockRecorder {
	return m.recorder
}

// Build mocks base method.
func (m *MockCIClient) Build(arg0 context.Context, arg1 *jenkins.Job) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Build", arg0, arg1)
	ret0, _ := ret[0].(error)
	return ret0
}

// Build indicates an expected call of Build.
func (mr *MockCIClientMockRecorder) Build(arg0, arg1 any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Build", reflect.TypeOf((*MockCIClient)(nil).Build), arg0, arg1)
}

// String mocks base method.
func (m *MockCIClient) String() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "String")
	ret0, _ := ret[0].(string)
	return ret0
}

// String indicates an expected call of String.
func (mr *MockCIClientMockRecorder) String() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "String", reflect.TypeOf((*MockCIClient)(nil).String))
}