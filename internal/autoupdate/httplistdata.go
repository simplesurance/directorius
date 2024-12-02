package autoupdate

import "time"

type httpListQueue struct {
	RepositoryOwner string
	Repository      string
	BaseBranch      string

	ActivePRs    []*PullRequest
	SuspendedPRs []*PullRequest
}

// httpListData is used as template data when rending the autoupdater list
// page.
type httpListData struct {
	Queues                       []*httpListQueue
	TriggerOnAutomerge           bool
	TriggerLabels                []string
	MonitoredRepositoriesitories []string
	PeriodicTriggerInterval      time.Duration
	ProcessedEvents              uint64
	CIServer                     string
	CIJobURLs                    []string

	// CreatedAt is the time when this datastructure was created.
	CreatedAt time.Time
}

func (a *Autoupdater) httpListData() *httpListData {
	result := httpListData{CreatedAt: time.Now()}

	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()

	result.TriggerOnAutomerge = a.TriggerOnAutomerge

	for k := range a.TriggerLabels {
		result.TriggerLabels = append(result.TriggerLabels, k)
	}

	for k := range a.MonitoredRepositories {
		result.MonitoredRepositoriesitories = append(result.MonitoredRepositoriesitories, k.String())
	}

	result.PeriodicTriggerInterval = a.periodicTriggerIntv
	result.ProcessedEvents = a.processedEventCnt.Load()

	if a.Config.CI == nil {
		result.CIServer = "undefined"
	} else {
		result.CIServer = a.Config.CI.Client.String()
	}

	for _, j := range a.Config.CI.Jobs {
		result.CIJobURLs = append(result.CIJobURLs, j.RelURL)
	}

	for baseBranch, queue := range a.queues {
		queueData := httpListQueue{
			RepositoryOwner: baseBranch.RepositoryOwner,
			Repository:      baseBranch.Repository,
			BaseBranch:      baseBranch.Branch,
		}

		queueData.ActivePRs, queueData.SuspendedPRs = queue.asSlices()

		result.Queues = append(result.Queues, &queueData)
	}

	return &result
}
