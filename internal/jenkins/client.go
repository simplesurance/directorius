package jenkins

import (
	"net/http"
	"time"

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

const requestTimeout = time.Minute

const userAgent = "directorius"

func NewClient(url, user, password string) *Client {
	return &Client{
		url:    url,
		auth:   &basicAuth{user: user, password: password},
		clt:    &http.Client{Timeout: requestTimeout},
		logger: zap.L().Named("jenkins_client"),
	}
}

func (s *Client) String() string {
	return s.url
}
