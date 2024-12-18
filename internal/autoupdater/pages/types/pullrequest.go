package types

import "strconv"

type PullRequest struct {
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

func PRPriorityOptions(prPriority int32) []*Option {
	options := make([]*Option, 0, 5)
	for i := -2; i < 3; i++ {
		options = append(options, priorityOption(int32(i), prPriority))
	}
	return options
}

func priorityOption(priority, prPriority int32) *Option {
	return &Option{
		Value:    priorityToString(priority),
		Selected: priority == prPriority,
	}
}

func priorityToString(p int32) string {
	switch p {
	case 2:
		return "++"
	case 1:
		return "+"
	case 0:
		return "o"
	case -1:
		return "-"
	case -2:
		return "--"
	default:
		return strconv.FormatInt(int64(p), 32)
	}
}
