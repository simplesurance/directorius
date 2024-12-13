package autoupdater

import (
	"go.uber.org/zap"
)

var logReasonPRClosed = logFieldReason("pull_request_closed")

func logFieldReason(reason string) zap.Field {
	return zap.String("reason", reason)
}
