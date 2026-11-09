package githubclt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shurcooL/githubv4"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestOverallCIStatus_optionalFailedChecksAreIgnored(t *testing.T) {
	status := overallCIStatus(
		githubv4.StatusStateError,
		[]*CIJobStatus{
			{
				Name:     "required_check",
				Status:   CIStatusSuccess,
				Required: true,
			},
			{
				Name:     "optional_check",
				Status:   CIStatusFailure,
				Required: false,
			},
		},
	)

	require.Equal(t, CIStatusSuccess, status)
}

func TestOverallCIStatus_optionalPendingChecksAreHonored(t *testing.T) {
	status := overallCIStatus(
		githubv4.StatusStateError,
		[]*CIJobStatus{
			{
				Name:     "optional_check",
				Status:   CIStatusPending,
				Required: false,
			},
			{
				Name:     "required_check",
				Status:   CIStatusSuccess,
				Required: true,
			},
		},
	)

	require.Equal(t, CIStatusSuccess, status)
}

func TestOverallCIStatus_requiredFailedCheck(t *testing.T) {
	status := overallCIStatus(
		githubv4.StatusStateError,
		[]*CIJobStatus{
			{
				Name:     "optional_check",
				Status:   CIStatusPending,
				Required: false,
			},
			{
				Name:     "required_check",
				Status:   CIStatusFailure,
				Required: true,
			},
			{
				Name:     "required_check1",
				Status:   CIStatusSuccess,
				Required: true,
			},
		},
	)

	require.Equal(t, CIStatusFailure, status)
}

func TestOverallCIStatus_missingRequiredCheckWinsOverPendingRollup(t *testing.T) {
	status := overallCIStatus(
		githubv4.StatusStatePending,
		[]*CIJobStatus{
			{
				Name:     "optional_check",
				Status:   CIStatusPending,
				Required: false,
			},
			{
				Name:     "required_check",
				Status:   CIStatusExpected,
				Required: true,
			},
		},
	)

	require.Equal(t, CIStatusExpected, status)
}

func TestOverallCIStatus_pendingRollupWithoutMissingRequiredCheck(t *testing.T) {
	status := overallCIStatus(
		githubv4.StatusStatePending,
		[]*CIJobStatus{
			{
				Name:     "optional_check",
				Status:   CIStatusPending,
				Required: false,
			},
			{
				Name:     "required_check",
				Status:   CIStatusSuccess,
				Required: true,
			},
		},
	)

	require.Equal(t, CIStatusPending, status)
}

func TestOverallCIStatus_Expected(t *testing.T) {
	status := overallCIStatus(
		githubv4.StatusStateError,
		[]*CIJobStatus{
			{
				Name:     "expected_req_check",
				Status:   CIStatusExpected,
				Required: true,
			},
			{
				Name:     "required_check",
				Status:   CIStatusFailure,
				Required: false,
			},
			{
				Name:     "required_check1",
				Status:   CIStatusSuccess,
				Required: true,
			},

			{
				Name:     "pending_check",
				Status:   CIStatusPending,
				Required: true,
			},
		},
	)

	require.Equal(t, CIStatusExpected, status)
}

// readyForMergeResponse returns the response of the GraphQL query of
// reviewAndCIStatus for a PR that has a single successful required check.
// prFields contains additional pullRequest fields in JSON format.
func readyForMergeResponse(prFields string) string {
	return `{"data":{"repository":{"pullRequest":{
		"reviewDecision":"APPROVED",
		` + prFields + `,
		"baseRef":{"branchProtectionRule":{"requiredStatusCheckContexts":["build"]}},
		"commits":{"nodes":[{"commit":{
			"oid":"abc123",
			"statusCheckRollup":{
				"state":"SUCCESS",
				"contexts":{
					"pageInfo":{"endCursor":"","hasNextPage":false},
					"edges":[{"node":{
						"name":"build",
						"conclusion":"SUCCESS",
						"status":"COMPLETED",
						"detailsUrl":"http://localhost/job/build/1"
					}}]
				}
			}
		}}]}
	}}}}`
}

func newReadyForMergeTestClient(t *testing.T, response string) (clt *Client, requestBody func() string) {
	t.Helper()

	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body failed: %v", err)
		}
		body = string(b)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(srv.Close)

	return &Client{
		logger:     zaptest.NewLogger(t),
		graphQLClt: githubv4.NewEnterpriseClient(srv.URL, srv.Client()),
	}, func() string { return body }
}

func TestReadyForMerge_ReturnsAutoMergeLabelsAndOpenState(t *testing.T) {
	clt, requestBody := newReadyForMergeTestClient(t, readyForMergeResponse(`
		"closed":false,
		"autoMergeRequest":{"enabledAt":"2026-01-02T03:04:05Z"},
		"labels":{
			"pageInfo":{"endCursor":"","hasNextPage":false},
			"nodes":[{"name":"queue-add"},{"name":"bug"}]
		}`,
	))

	status, err := clt.ReadyForMerge(context.Background(), "owner", "repo", 1)
	require.NoError(t, err)

	require.False(t, status.Closed)
	require.True(t, status.AutoMergeEnabled)
	require.Equal(t, []string{"queue-add", "bug"}, status.Labels)
	require.Equal(t, ReviewDecisionApproved, status.ReviewDecision)
	require.Equal(t, CIStatusSuccess, status.CIStatus)
	require.Equal(t, "abc123", status.Commit)

	// the fields must be part of the query
	for _, field := range []string{"closed", "autoMergeRequest{enabledAt}", "labels(first: 100)"} {
		require.Contains(t, requestBody(), field)
	}
}

func TestReadyForMerge_AutoMergeDisabledAndNoLabels(t *testing.T) {
	// GitHub returns null as autoMergeRequest if auto-merge is not enabled
	clt, _ := newReadyForMergeTestClient(t, readyForMergeResponse(`
		"closed":false,
		"autoMergeRequest":null,
		"labels":{
			"pageInfo":{"endCursor":"","hasNextPage":false},
			"nodes":[]
		}`,
	))

	status, err := clt.ReadyForMerge(context.Background(), "owner", "repo", 1)
	require.NoError(t, err)

	require.False(t, status.Closed)
	require.False(t, status.AutoMergeEnabled)
	require.Empty(t, status.Labels)
}

func TestReadyForMerge_ClosedPR(t *testing.T) {
	clt, _ := newReadyForMergeTestClient(t, readyForMergeResponse(`
		"closed":true,
		"autoMergeRequest":null,
		"labels":{
			"pageInfo":{"endCursor":"","hasNextPage":false},
			"nodes":[]
		}`,
	))

	status, err := clt.ReadyForMerge(context.Background(), "owner", "repo", 1)
	require.NoError(t, err)

	require.True(t, status.Closed)
}
