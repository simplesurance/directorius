package githubclt

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v67/github"

	"github.com/simplesurance/directorius/internal/goorderr"

	"github.com/shurcooL/githubv4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestWrapRetryableErrorsGraphql(t *testing.T) {
	t.Cleanup(zap.ReplaceGlobals(zaptest.NewLogger(t).Named(t.Name())))

	// is the same then in vendor/github.com/shurcooL/graphql/graphql.go do()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	t.Cleanup(srv.Close)

	clt := Client{
		logger:     zap.L(),
		graphQLClt: githubv4.NewEnterpriseClient(srv.URL, srv.Client()),
	}

	s, err := clt.ReadyForMerge(context.Background(), "test", "test", 123)
	require.Error(t, err)
	assert.Nil(t, s)

	var retryableErr *goorderr.RetryableError
	assert.ErrorAs(t, err, &retryableErr)
}

func TestWrapRetryableErrorsGraphqlWithNonStatusErr(t *testing.T) {
	err := errors.New("error")
	wrappedErr := (&Client{}).wrapGraphQLRetryableErrors(err)
	assert.Equal(t, err, wrappedErr)
}

func TestUpdateBranch_SuccessWhenBranchIsAlreadyUpToDate(t *testing.T) {
	t.Cleanup(zap.ReplaceGlobals(zaptest.NewLogger(t).Named(t.Name())))

	mux := http.NewServeMux()

	// Mock for the initial PRIsUptodate check, which gets the pull request.
	// We return "mergeable_state": "behind" to ensure the client proceeds to the update step.
	mux.HandleFunc("/repos/owner/repo/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		fmt.Fprint(w, `{"head": {"sha": "test-sha"}, "base": {"ref": "main"}, "mergeable_state": "behind", "state": "open"}`)
	})

	// Mock for the update-branch call itself.
	// This is the core of the test: we simulate the specific 422 error that should be handled as a success.
	mux.HandleFunc("/repos/owner/repo/pulls/1/update-branch", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message": "There are no new commits on the base branch."}`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Create a new github client that uses the test server
	restClient := github.NewClient(srv.Client())
	url, err := url.Parse(srv.URL + "/")
	require.NoError(t, err)
	restClient.BaseURL = url

	clt := &Client{
		restClt:    restClient,
		graphQLClt: nil, // Not used in this code path
		logger:     zap.L(),
	}

	result, err := clt.UpdateBranch(context.Background(), "owner", "repo", 1)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "test-sha", result.HeadCommitID)
	assert.False(t, result.Changed, "Result.Changed should be false")
	assert.False(t, result.Scheduled, "Result.Scheduled should be false")
}
