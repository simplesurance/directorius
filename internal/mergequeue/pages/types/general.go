package types

import "time"

type Link struct {
	Text string
	URL  string
}

type Option struct {
	ID       string
	Value    string
	Text     string
	Selected bool
}

// OptionValueSelected is the value of a select element that is used when the option represents the current value for setting.
// When the value is submitted in a form, settings that have these values do
// not need to be updated.
const OptionValueSelected = "unchanged"

func TimeSince(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return time.Since(t).Round(time.Second).String()
}
