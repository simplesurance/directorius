package types

import "strconv"

type PullRequest struct {
	Number             string
	Priority           []*Option
	Link               *Link
	Author             *Link
	EnqueuedSince      string
	InActiveQueueSince string
	Suspensions        uint32
	Status             string
}

func PRStatus(isFirstInActiveQueue bool) string {
	if isFirstInActiveQueue {
		return "processing"
	}

	return "queued"
}

func PRPriorityOptions(prNumber int, prPriority int32) []*Option {
	options := make([]*Option, 0, 5)
	for i := 2; i > -2; i-- {
		options = append(options, priorityOption(prNumber, int32(i), prPriority))
	}
	return options
}

func priorityOption(prNumber int, priority, currentPRPriority int32) *Option {
	var val string

	if priority == currentPRPriority {
		val = OptionValueSelected
	} else {
		val = strconv.Itoa(int(priority))
	}

	return &Option{
		ID:       strconv.Itoa(prNumber),
		Text:     priorityToString(priority),
		Value:    val,
		Selected: priority == currentPRPriority,
	}
}

func priorityToString(p int32) string {
	switch p {
	case 2:
		return `🚨`
	case 1:
		return `🏃`
	case 0:
		return `⚖️`
	case -1:
		return `🐌`
	default:
		return strconv.FormatInt(int64(p), 32)
	}
}
