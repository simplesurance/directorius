package jenkins

import (
	"context"
	"encoding/json"
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

		if !assert.Equal(t, "/build/mybranch", r.URL.Path) {
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

		paramsJSON := urlVals.Get("json")
		if !assert.NotEmpty(t, paramsJSON) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var params jenkinsParameters

		err = json.Unmarshal([]byte(paramsJSON), &params)
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !assert.Len(t, params.Parameter, 2) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		p1 := params.Parameter[0]
		var pVerFound, pBranchFound bool
		for _, p := range params.Parameter {
			switch p.Name {
			case "version":
				if !assert.Equal(t, "123", p.Value) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				pVerFound = true
			case "branch":
				if !assert.Equal(t, "mybranch", p.Value) {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				pBranchFound = true
			default:
				t.Error("unexpected parameter", p1.Name)
				w.WriteHeader(http.StatusBadRequest)
				return

			}
		}

		if !assert.True(t, pVerFound) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if !assert.True(t, pBranchFound) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	clt := NewClient(srv.URL, "", "")
	jt := JobTemplate{
		RelURL:     "build/{{ .Branch }}",
		Parameters: map[string]string{"version": "123", "branch": "{{ .Branch }}"},
	}
	job, err := jt.Template(TemplateData{PullRequestNumber: "123", Branch: "mybranch"})
	require.NoError(t, err)

	err = clt.Build(context.Background(), job)
	require.NoError(t, err)

	srv.Close()
}
