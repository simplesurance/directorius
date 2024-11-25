package jenkins

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type basicAuth struct {
	user     string
	password string
}

type Server struct {
	url  string
	auth *basicAuth

	clt *http.Client
}

const (
	requestTimeout = time.Minute
	userAgent      = "directorius"
)

func NewServer(url, user, password string) *Server {
	return &Server{
		url:  url,
		auth: &basicAuth{user: user, password: password},
		clt:  &http.Client{Timeout: requestTimeout},
	}
}

func (s *Server) Build(ctx context.Context, j *Job) error {
	// https://wiki.jenkins-ci.org/display/JENKINS/Remote+access+API
	url, err := url.JoinPath(s.url, j.relURL)
	if err != nil {
		return fmt.Errorf("concatening server (%q) with job (%q) url failed: %w", s.url, j.relURL, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, toRequestBody(j))
	if err != nil {
		return fmt.Errorf("creating http-request failed: %w", err)
	}

	req.Header.Add("User-Agent", userAgent)
	// TODO: add accept header
	// TODO: set content-type
	// s.clt.Do(req)
	return nil
}

func toRequestBody(j *Job) io.Reader {
	if len(j.parametersJSON) == 0 {
		return nil
	}

	return bytes.NewReader(j.parametersJSON)
}
