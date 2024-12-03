package jenkins

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/simplesurance/directorius/internal/goorderr"

	"go.uber.org/zap"
)

type Client struct {
	url  string
	auth *basicAuth

	clt    *http.Client
	logger *zap.Logger
}

type basicAuth struct {
	user     string
	password string
}

const (
	requestTimeout = time.Minute
	userAgent      = "directorius"
)

func NewClient(url, user, password string) *Client {
	return &Client{
		url:    url,
		auth:   &basicAuth{user: user, password: password},
		clt:    &http.Client{Timeout: requestTimeout},
		logger: zap.L().Named("jenkins_client"),
	}
}

func (s *Client) Build(ctx context.Context, j *Job) error {
	// https://wiki.jenkins-ci.org/display/JENKINS/Remote+access+API
	url, err := url.JoinPath(s.url, j.relURL)
	if err != nil {
		return fmt.Errorf("concatening server (%q) with job (%q) url failed: %w", s.url, j.relURL, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, toRequestBody(j))
	if err != nil {
		return fmt.Errorf("creating http-request failed: %w", err)
	}

	if req.Body != nil {
		req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	}

	req.Header.Add("User-Agent", userAgent)

	req.SetBasicAuth(s.auth.user, s.auth.password)

	resp, err := s.clt.Do(req)
	if err != nil {
		return goorderr.NewRetryableAnytimeError(err)
	}

	defer resp.Body.Close()
	if resp.ProtoMajor == 1 {
		defer func() {
			// try to drain body but limit it to a non-excessive amount
			_, _ = io.CopyN(io.Discard, resp.Body, 1024)
		}()
	}

	switch code := resp.StatusCode; {
	case code == http.StatusCreated:
		// Jenkins returns 201 and sends in the Location header the URL of the queued item,
		// it's url can be used to get the build id, query the status, cancel it, etc
		// location := resp.Header.Get("Location")
		return nil
	case code == http.StatusSeeOther, code == http.StatusFound:
		// build already exists, probably happens when triggering a job
		// with the same parameters then one in the wait-queue
		return nil

	case code >= 200 && code < 300:
		s.logger.Debug("server returned unexpected status code, interpreting it as success",
			zap.Int("http.status_code", resp.StatusCode),
			zap.String("http.request_url", req.URL.Redacted()),
		)
		return nil
	default:
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
		return goorderr.NewRetryableAnytimeError(fmt.Errorf("server returned status code: %d", resp.StatusCode))

	}
}

func toRequestBody(j *Job) io.Reader {
	if len(j.parametersJSON) == 0 {
		return nil
	}

	formData := url.Values{
		"json": []string{string(j.parametersJSON)},
	}

	return strings.NewReader(formData.Encode())
}

func (s *Client) String() string {
	return s.url
}
