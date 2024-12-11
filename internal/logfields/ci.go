package logfields

import "go.uber.org/zap"

func CIJob(name string) zap.Field {
	return zap.String("ci.job", name)
}

func CIBuildURL(url string) zap.Field {
	return zap.String("ci.build.url", url)
}
