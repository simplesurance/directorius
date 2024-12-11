package jenkins

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/simplesurance/directorius/internal/goorderr"
	"github.com/simplesurance/directorius/internal/logfields"
)

// Build schedules a build of the job.
// On success it returns the ID of the queued build item and nil.
// On errors where retrying might lead to positive result a
// [goorderr.RetryableError] is returned.
func (s *Client) Build(ctx context.Context, j *Job) (int64, error) {
	// https://wiki.jenkins-ci.org/display/JENKINS/Remote+access+API
	// https://www.jenkins.io/doc/book/using/remote-access-api/

	req, err := s.newBuildRequest(ctx, j)
	if err != nil {
		return -1, err
	}

	resp, err := s.Do(req)
	if err != nil {
		return -1, goorderr.NewRetryableAnytimeError(err)
	}

	defer drainCloseBody(resp)

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
		return -1, goorderr.NewRetryableAnytimeError(fmt.Errorf("server returned status code: %d", resp.StatusCode))
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
			logfields.HTTPResponseStatusCode(resp.StatusCode),
			logfields.HTTPRequestURL(req.URL.Redacted()),
		)
	}
	id, err := queueItemIDFromLocationHeader(resp.Header)
	if err != nil {
		return -1, fmt.Errorf("extracting queue item id from location header of response with status code %d failed: %w", resp.StatusCode, err)
	}

	return id, nil
}

func queueItemIDFromLocationHeader(hdr http.Header) (int64, error) {
	location := hdr.Get("Location")
	if location == "" {
		return -1, errors.New("location header is missing")
	}

	//  https://jenkins.localhost/queue/item/6482513/
	locURL, err := url.Parse(location)
	if err != nil {
		return -1, fmt.Errorf("location header value (%s) can not be parsed as url: %w", location, err)
	}

	const queueURLPathPrefix = "/queue/item"
	if !strings.HasPrefix(locURL.Path, queueURLPathPrefix) {
		return -1, fmt.Errorf("location header value (%s) does not start with %s", location, queueURLPathPrefix)
	}

	itemIDStr := path.Base(locURL.Path)
	itemID, err := strconv.ParseInt(itemIDStr, 10, 64)
	if err != nil {
		return -1, fmt.Errorf("converting last url path element (%s) to an integer item id failed: %w", itemIDStr, err)
	}

	return itemID, nil
}

func (s *Client) newBuildRequest(ctx context.Context, j *Job) (*http.Request, error) {
	hasParams := len(j.parameters) > 0

	reqURL := s.url.JoinPath(j.relURL, getBuildEndpoint(hasParams))
	queryParams := make(url.Values, 1)
	queryParams.Add("delay", "0sec")
	reqURL.RawQuery = queryParams.Encode()

	req, err := s.newRequest(ctx, http.MethodPost, reqURL.String(), toRequestBody(j))
	if err != nil {
		return nil, err
	}

	if req.Body != nil {
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	}

	return req, nil
}

func getBuildEndpoint(hasParameters bool) string {
	if hasParameters {
		return "buildWithParameters"
	}

	return "build"
}
