package logfields

import "go.uber.org/zap"

func CIJob(name string) zap.Field {
	return zap.String("ci.job", name)
}
