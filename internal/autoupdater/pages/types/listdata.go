package types

import "time"

type ListData struct {
	Queues                       []*Queue
	TriggerOnAutomerge           bool
	TriggerLabels                []string
	MonitoredRepositoriesitories []string
	PeriodicTriggerInterval      time.Duration
	ProcessedEvents              uint64
	CIServer                     string
	CIJobURLs                    []string
	PriorityChangePostURL        string

	// CreatedAt is the time when this datastructure was created.
	CreatedAt time.Time
}
