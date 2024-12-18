package types

import "time"

type Link struct {
	Name string
	URL  string
}

type Option struct {
	Value    string
	Selected bool
}

func TimeSince(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return time.Since(t).Round(time.Minute).String()
}
