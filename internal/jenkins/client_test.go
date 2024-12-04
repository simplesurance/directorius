package jenkins

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunJobWithParameters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		assert.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		assert.Positive(t, r.ContentLength)

		if !assert.Equal(t, "/job/mybranch/buildWithParameters", r.URL.Path) {
			w.WriteHeader(http.StatusBadRequest)
			return // required because we can't use require in go-routines
		}

		body, err := io.ReadAll(r.Body)
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusBadRequest)
			return // required because we can't use require in go-routines
		}

		urlVals, err := url.ParseQuery(string(body))
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if !assert.Len(t, urlVals, 2) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		v := urlVals.Get("version")
		if !assert.Equal(t, "123", v) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		branch := urlVals.Get("branch")
		if !assert.Equal(t, "mybranch", branch) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.Header().Add("Location", "https://localhost/queue/item/123")
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	clt := NewClient(srv.URL, "", "")
	jt := JobTemplate{
		RelURL:     "job/{{ .Branch }}",
		Parameters: map[string]string{"version": "123", "branch": "{{ .Branch }}"},
	}
	job, err := jt.Template(TemplateData{PullRequestNumber: "123", Branch: "mybranch"})
	require.NoError(t, err)

	err = clt.Build(context.Background(), job)
	require.NoError(t, err)

	srv.Close()
}
