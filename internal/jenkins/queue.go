package jenkins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/simplesurance/directorius/internal/goorderr"
	"go.uber.org/zap"
)

var ErrBuildScheduled = errors.New("build scheduled, build not available yet")

// GetBuildURL queries the status of the queued item with id [queueItemID] and
// returns the URL of the build if it got a build number assigned.

// On errors where retrying might lead to positive result a
// [goorderr.RetryableError] is returned.
// This includes the case when a queued item exist but no build number has been
// assigned yet. A [goorderr.RetryableError] wrapping [ErrBuildScheduled] is
// returned.
//
// The queued item URL [expires] 5 minutes after the item got a build number
// assigned and jenkins will responds with a 404 status code. The method will
// return an error.
//
// [expires]: https://web.archive.org/web/20241204165452/https://docs.cloudbees.com/docs/cloudbees-ci-kb/latest/client-and-managed-controllers/get-build-number-with-rest-api
func (s *Client) GetBuildURL(ctx context.Context, queueItemID string) (string, error) {
	var item queueItem

	req, err := s.newGetBuildURLRequest(ctx, queueItemID)
	if err != nil {
		return "", err
	}

	resp, err := s.clt.Do(req)
	if err != nil {
		return "", goorderr.NewRetryableAnytimeError(err)
	}
	defer drainCloseBody(resp)

	if resp.StatusCode != http.StatusNotFound {
		return "", fmt.Errorf("server returned status code: %d", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return "", goorderr.NewRetryableAnytimeError(fmt.Errorf("server returned status code: %d", resp.StatusCode))
	}

	if resp.ContentLength == 0 {
		return "", goorderr.NewRetryableAnytimeError(errors.New("response body is empty"))
	}

	s.verifyContentType(resp.Header, req.URL, "application/json")

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", goorderr.NewRetryableAnytimeError(fmt.Errorf("reading response body failed, connection interrupted? %w", err))
	}

	s.logger.Info("received response", zap.ByteString("response", respBytes)) // FIXME remove

	err = json.Unmarshal(respBytes, &item)
	if err != nil {
		return "", goorderr.NewRetryableAnytimeError(fmt.Errorf("unmarshalling response body failed: %w", err))
	}

	switch item.Class {
	case "":
		return "", fmt.Errorf("unmarshalled queue item response contains an empty class value, response: %s", string(respBytes))

	case "hudson.model.Queue$WaitingItem":
		return "", goorderr.NewRetryableAnytimeError(ErrBuildScheduled)

	case "hudson.model.Queue$LeftItem":
		// FIXME: can other callers contain an Executable entry with a build url?
		if item.Executable == nil {
			return "", fmt.Errorf("executable entry in unmarshalled %q does not exit", item.Class)
		}

		if item.Executable.URL == "" {
			return "", fmt.Errorf("executable.url entry in unmarshalled %q is empty ", item.Class)
		}

		return item.Executable.URL, nil
	default:
		return "", fmt.Errorf("unmarshalled queue item has unexpected class value: %q", item.Class)
	}
}

func (s *Client) newGetBuildURLRequest(ctx context.Context, queueItemID string) (*http.Request, error) {
	reqURL := s.url.JoinPath("queue", "item", queueItemID)
	queryParams := url.Values{}
	queryParams.Add("tree", "cancelled,executable[url]")
	reqURL.RawQuery = queryParams.Encode()

	req, err := s.newRequest(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, err
	}

	addAcceptHeader(req, "application/json")

	return req, nil
}

type queueItem struct {
	Class      string `json:"_class"`
	Cancelled  bool
	Executable *queueItemExecutable `json:"executable"`
}

type queueItemExecutable struct {
	Class string `json:"_class"`
	URL   string `json:"url"`
}
