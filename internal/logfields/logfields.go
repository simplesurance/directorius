package logfields

import "go.uber.org/zap"

func Operation(val string) zap.Field {
	return zap.String("operation", val)
}
