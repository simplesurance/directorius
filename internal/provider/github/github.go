package github

import (
	"net/http"

	"github.com/google/go-github/v67/github"

	"github.com/simplesurance/directorius/internal/logfields"

	"go.uber.org/zap"
)

const loggerName = "github-event-provider"

// Provider listens for github-webhook http-requests at a http-server handler.
// It validates, parses the webhook events and forwards them to event channels.
type Provider struct {
	logging       *zap.Logger
	webhookSecret []byte
	chans         []chan<- *Event
}

type option func(*Provider)

func WithPayloadSecret(secret string) option { // nolint:revive // returning unexported field is fine here
	return func(p *Provider) {
		p.webhookSecret = []byte(secret)
	}
}

func New(eventChans []chan<- *Event, opts ...option) *Provider {
	p := Provider{
		chans: eventChans,
	}

	for _, o := range opts {
		o(&p)
	}

	p.logging = zap.L().Named(loggerName)

	return &p
}

func (p *Provider) HTTPHandler(resp http.ResponseWriter, req *http.Request) {
	deliveryID := github.DeliveryID(req)
	hookType := github.WebHookType(req)

	logFields := []zap.Field{
		logfields.EventProvider("github"),
		zap.String("github.delivery_id", deliveryID),
		zap.String("github.webhook_type", hookType),
	}

	logger := p.logging.With(logFields...)
	logger.Debug("received a http request")

	payload, err := github.ValidatePayload(req, p.webhookSecret)
	if err != nil {
		logger.Info(
			"received invalid http request, payload validation failed",
			zap.Error(err),
		)
		http.Error(resp, err.Error(), http.StatusBadRequest)
		return
	}

	event, err := github.ParseWebHook(github.WebHookType(req), payload)
	if err != nil {
		logger.Info(
			"received invalid http request, parsing failed",
			zap.Error(err),
		)
		http.Error(resp, err.Error(), http.StatusBadRequest)
		return
	}

	ev := Event{
		DeliveryID: deliveryID,
		Type:       hookType,
		JSON:       payload,
		Event:      event,
		LogFields:  logFields,
	}

	for i, ch := range p.chans {
		logger = logger.With(zap.Int("chan_idx", i))

		select {
		case ch <- &ev:
			logger.Debug("event forwarded to channel")

		default:
			logger.Warn(
				"event lost, forwarding event to channel failed",
				zap.String("error", "could not forward event to channel, send would have blocked"),
			)
		}
	}
}
