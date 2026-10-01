package mergequeue

import (
	"slices"
	"strconv"
	"time"

	"github.com/simplesurance/directorius/internal/mergequeue/pages/pagestypes"
)

func (a *Coordinator) httpListData() *pagestypes.ListData {
	result := pagestypes.ListData{
		CreatedAt:             time.Now(),
		PriorityChangePostURL: handlerPriorityUpdatePath,
		SuspendResumePostURL:  handlerSuspendResumePath,
	}

	a.queuesLock.Lock()
	defer a.queuesLock.Unlock()

	result.TriggerOnAutomerge = a.TriggerOnAutomerge

	for k := range a.TriggerLabels {
		result.TriggerLabels = append(result.TriggerLabels, k)
	}

	for k := range a.MonitoredRepositories {
		result.MonitoredRepositories = append(result.MonitoredRepositories, k.String())
	}

	result.PeriodicTriggerInterval = a.periodicTriggerIntv
	result.ProcessedEvents = a.processedEventCnt.Load()

	if a.CI == nil {
		result.CIServer = "undefined"
	} else {
		result.CIServer = a.CI.Client.String()
	}

	result.CIJobs = make(map[string]string, len(a.CI.Jobs))
	for context, j := range a.CI.Jobs {
		result.CIJobs[context] = j.RelURL
	}

	for baseBranch, queue := range a.queues {
		queueData := pagestypes.Queue{
			RepositoryOwner: baseBranch.RepositoryOwner,
			Repository:      baseBranch.Repository,
			BaseBranch:      baseBranch.Branch,
			Paused:          queue.IsPaused(),
		}

		activePRs, suspendedPRs := queue.asSlices()

		// sort them PRs to show list in a stable order in the UI
		slices.SortFunc(suspendedPRs, orderBefore)

		queueData.ActivePRs = toPagesPullRequests(activePRs)
		queueData.SuspendedPRs = toPagesPullRequests(suspendedPRs)

		result.Queues = append(result.Queues, &queueData)
	}

	return &result
}

func toPagesPullRequests(prs []*PullRequest) []*pagestypes.PullRequest {
	result := make([]*pagestypes.PullRequest, 0, len(prs))
	for i, pr := range prs {
		result = append(result, toPagesPullRequest(pr, i == 0))
	}

	return result
}

func toPagesPullRequest(pr *PullRequest, isFirst bool) *pagestypes.PullRequest {
	return &pagestypes.PullRequest{
		Number:   strconv.Itoa(pr.Number),
		Priority: pagestypes.PRPriorityOptions(pr.Number, pr.Priority.Load()),
		Link: &pagestypes.Link{
			Text: pr.Title,
			URL:  pr.Link,
		},
		Author:             pagestypes.NewPerson(pr.Author),
		Assignees:          pagestypes.NewPersons(pr.Assignees()),
		EnqueuedSince:      pagestypes.TimeSince(pr.EnqueuedAt),
		InActiveQueueSince: pagestypes.TimeSince(pr.InActiveQueueSince()),
		Suspensions:        pr.SuspendCount.Load(),
		Status:             pagestypes.PRStatus(isFirst),
	}
}
