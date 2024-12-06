package logfields

import "go.uber.org/zap"

func HTTPRequestURL(url string) zap.Field {
	return zap.String("http.request.url", url)
}

func HTTPResponseStatusCode(code int) zap.Field {
	return zap.Int("http.response.status_code", code)
}
