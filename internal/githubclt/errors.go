package githubclt

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/google/go-github/v67/github"
	"go.uber.org/zap"

	"github.com/simplesurance/directorius/internal/goorderr"
)

var ErrPullRequestIsClosed = errors.New("pull request is closed")

var graphQlHTTPStatusErrRe = regexp.MustCompile(`^non-200 OK status code: ([0-9]+) .*`)

func (clt *Client) wrapRetryableErrors(err error) error {
	switch v := err.(type) { // nolint:errorlint // errors.As not needed here
	case *github.RateLimitError:
		clt.logger.Info(
			"rate limit exceeded",
			zap.Int("github_api_rate_limit", v.Rate.Limit),
			zap.Int("github_api_rate_limit", v.Rate.Limit),
			zap.Time("github_api_rate_limit_reset_time", v.Rate.Reset.Time),
		)

		return goorderr.NewRetryableError(err, v.Rate.Reset.Time)

	case *github.ErrorResponse:
		if v.Response.StatusCode >= 500 && v.Response.StatusCode < 600 {
			return goorderr.NewRetryableAnytimeError(err)
		}
	}

	return err
}

func (clt *Client) wrapGraphQLRetryableErrors(err error) error {
	matches := graphQlHTTPStatusErrRe.FindStringSubmatch(err.Error())
	if len(matches) != 2 {
		return err
	}

	errcode, atoiErr := strconv.Atoi(matches[1])
	if atoiErr != nil {
		clt.logger.Info(
			"parsing http code from error string failed",
			zap.Error(atoiErr),
			zap.String("error_string", err.Error()),
			zap.String("http_errcode", matches[1]),
		)
		return err
	}

	if errcode >= 500 && errcode < 600 {
		return goorderr.NewRetryableAnytimeError(err)
	}

	return err
}

type ErrMergeConflict struct {
	Err error
}

func NewErrMergeConflict(cause error) error {
	return &ErrMergeConflict{
		Err: cause,
	}
}

func (e *ErrMergeConflict) Error() string {
	return fmt.Sprintf("merge conflict: %s", e.Err.Error())
}

func (e *ErrMergeConflict) Unwrap() error {
	return e.Err
}
