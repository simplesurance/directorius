package jenkins

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/simplesurance/directorius/internal/logfields"
	"go.uber.org/zap"
)

// Client is a HTTP API client for a Jenkins Server.
type Client struct {
	url  *url.URL
	auth *basicAuth

	clt    *http.Client
	logger *zap.Logger
}

type basicAuth struct {
	user     string
	password string
}

const requestTimeout = time.Minute

const userAgent = "directorius"

func NewClient(logger *zap.Logger, serverURL, user, password string) (*Client, error) {
	url, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}

	if logger == nil {
		logger = zap.L()
	}

	return &Client{
		url:    url,
		auth:   &basicAuth{user: user, password: password},
		clt:    &http.Client{Timeout: requestTimeout},
		logger: logger.Named("jenkins_client"),
	}, nil
}

func (s *Client) String() string {
	return s.url.Redacted()
}

func (s *Client) newRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	// TODO: set a default timeout when the context does not have one

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("creating http-request failed: %w", err)
	}

	req.Header.Add("User-Agent", userAgent)
	req.SetBasicAuth(s.auth.user, s.auth.password)

	return req, nil
}

func drainCloseBody(resp *http.Response) {
	if resp.ProtoMajor == 1 {
		// try to drain body but limit it to a non-excessive amount
		_, _ = io.CopyN(io.Discard, resp.Body, 1024)
	}

	_ = resp.Body.Close()
}

func addAcceptHeader(req *http.Request, mediaType string) {
	req.Header.Add("Accept", mediaType)
}

func (s *Client) Do(req *http.Request) (*http.Response, error) {
	s.logger.Debug("sending http-request",
		logfields.HTTPRequestURL(req.URL.Redacted()),
		zap.String("http.request.method", req.Method),
	)
	return s.clt.Do(req)
}

func (s *Client) verifyContentType(hdr http.Header, requestURL *url.URL, expectedContentType string) {
	const hdrKey = "Content-Type"

	contentType := hdr.Get(hdrKey)
	if contentType == "" {
		s.logger.Info("got response with empty "+hdrKey+"header",
			zap.String("expected_content_type", expectedContentType),
			zap.String("http.request_url", requestURL.Redacted()),
		)
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		s.logger.Info(
			"parsing response "+hdrKey+" header value as media-type failed",
			zap.String("http.request_url", requestURL.Redacted()),
			zap.Error(err),
		)
		return
	}

	if mt != expectedContentType {
		s.logger.Info(
			fmt.Sprintf("got response with %s header value %q, expecting content-type %q, continuing anyways", hdrKey, contentType, expectedContentType),
			zap.String("http.request_url", requestURL.Redacted()),
		)
	}
}
