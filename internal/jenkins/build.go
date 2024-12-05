package jenkins

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/simplesurance/directorius/internal/goorderr"

	"go.uber.org/zap"
)

// Build schedules a build of the job.
// On success it returns the URL to the queued build item and nil.
// The URL will [expire] 5 minutes after the item got a build number assigned.
//
// [expire]: https://web.archive.org/web/20241204165452/https://docs.cloudbees.com/docs/cloudbees-ci-kb/latest/client-and-managed-controllers/get-build-number-with-rest-api
func (s *Client) Build(ctx context.Context, j *Job) (string, error) {
	// https://wiki.jenkins-ci.org/display/JENKINS/Remote+access+API
	// https://www.jenkins.io/doc/book/using/remote-access-api/

	req, err := s.createRequest(ctx, j)
	if err != nil {
		return "", err
	}

	resp, err := s.clt.Do(req)
	if err != nil {
		return "", goorderr.NewRetryableAnytimeError(err)
	}

	defer resp.Body.Close()
	if resp.ProtoMajor == 1 {
		defer func() {
			// try to drain body but limit it to a non-excessive amount
			_, _ = io.CopyN(io.Discard, resp.Body, 1024)
		}()
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		/* we simply almost always retry to make it resilient,
		* requests can fail and succeed later e.g. on:
		- 404 because the multibranch job was not created yet but is soonish,
		- 502, 504 jenkins temporarily down,
		- 401: temporary issues with jenkins auth backend,
		- 403: because of the bug that we encounter, probably related
		       to github auth, where Jenkins from now and then fails with 403
		       in the UI and APIs and then works after some retries
		etc
		*/
		return "", goorderr.NewRetryableAnytimeError(fmt.Errorf("server returned status code: %d", resp.StatusCode))
	}

	switch resp.StatusCode {
	case http.StatusCreated:
		// Jenkins returns 201 and sends in the Location header the URL of the queued item,
		// it's url can be used to get the build id, query the status, cancel it, etc
		// location := resp.Header.Get("Location")
	case http.StatusSeeOther, http.StatusFound:
		// build already exists, probably happens when triggering a job
		// with the same parameters then one in the wait-queue
	default:
		s.logger.Debug("server returned unexpected status code, interpreting it as success",
			zap.Int("http.status_code", resp.StatusCode),
			zap.String("http.request_url", req.URL.Redacted()),
		)
	}

	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("server returned status code (%d) but the location header is missing", resp.StatusCode)
	}
	//  https://jenkins.localhost/queue/item/6482513/
	locURL, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("server returned status code (%d) with a location header (%s) that can not be parsed as url: %w", resp.StatusCode, location, err)
	}

	const queueURLPathPrefix = "/queue/item"
	if !strings.HasPrefix(locURL.Path, queueURLPathPrefix) {
		return "", fmt.Errorf("server returned status code (%d) with a location header (%s) does not start with %s", resp.StatusCode, location, queueURLPathPrefix)
	}

	return locURL.String(), nil
}

func (s *Client) createRequest(ctx context.Context, j *Job) (*http.Request, error) {
	hasParams := len(j.parameters) > 0
	reqURL, err := url.JoinPath(s.url, j.relURL, getBuildEndpoint(hasParams))
	if err != nil {
		return nil, fmt.Errorf("concatening server (%q) with job (%q) url failed: %w", s.url, j.relURL, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, toRequestBody(j))
	if err != nil {
		return nil, fmt.Errorf("creating http-request failed: %w", err)
	}

	if req.Body != nil {
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	}

	req.Header.Add("User-Agent", userAgent)
	req.SetBasicAuth(s.auth.user, s.auth.password)

	return req, nil
}

func getBuildEndpoint(hasParameters bool) string {
	if hasParameters {
		return "buildWithParameters"
	}

	return "build"
}

func toRequestBody(j *Job) io.Reader {
	if len(j.parameters) == 0 {
		return nil
	}

	formData := make(url.Values, len(j.parameters))
	for k, v := range j.parameters {
		formData.Set(k, v)
	}

	return strings.NewReader(formData.Encode())
}
