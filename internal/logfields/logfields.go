package logfields

import (
	"go.uber.org/zap"
)

func Operation(val string) zap.Field {
	return zap.String("operation", val)
}

// NewWith returns a new slice containing all passed [zap.Field]s.
func NewWith(fields []zap.Field, field ...zap.Field) []zap.Field {
	return append(fields, field...)
}
