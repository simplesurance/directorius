package autoupdater

import (
	"fmt"
	"net/url"
	"time"

	"github.com/simplesurance/directorius/internal/autoupdater/pages/types"
)

func (a *Autoupdater) httpListData() *types.ListData {
	result := types.ListData{CreatedAt: time.Now()}

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

	if a.CI == nil {
		result.CIServer = "undefined"
	} else {
		result.CIServer = a.CI.Client.String()
	}

	for _, j := range a.CI.Jobs {
		result.CIJobURLs = append(result.CIJobURLs, j.RelURL)
	}

	for baseBranch, queue := range a.queues {
		queueData := types.Queue{
			RepositoryOwner: baseBranch.RepositoryOwner,
			Repository:      baseBranch.Repository,
			BaseBranch:      baseBranch.Branch,
		}

		activePRs, suspendedPRs := queue.asSlices()

		queueData.ActivePRs = toPagesPullRequests(activePRs)
		queueData.SuspendedPRs = toPagesPullRequests(suspendedPRs)

		result.Queues = append(result.Queues, &queueData)
	}

	return &result
}

func toPagesPullRequests(prs []*PullRequest) []*types.PullRequest {
	result := make([]*types.PullRequest, 0, len(prs))
	for i, pr := range prs {
		result = append(result, toPagesPullRequest(pr, i == 0))
	}
	return result
}

func toPagesPullRequest(pr *PullRequest, isFirst bool) *types.PullRequest {
	return &types.PullRequest{
		Priority: types.PRPriorityOptions(int32(pr.Priority.Load())),
		Link: &types.Link{
			Text: fmt.Sprintf("%s (#%d)", pr.Title, pr.Number),
			URL:  pr.Link,
		},
		Author: &types.Link{
			Text: pr.Author,
			URL:  urlJoin("https://github.com", pr.Author),
		},
		EnqueuedSince:      types.TimeSince(pr.EnqueuedAt),
		InActiveQueueSince: types.TimeSince(pr.InActiveQueueSince()),
		Suspensions:        pr.SuspendCount.Load(),
		Status:             types.PRStatus(isFirst),
	}
}

func urlJoin(base string, elem ...string) string {
	result, err := url.JoinPath(base, elem...)
	if err != nil {
		return ""
	}
	return result
}
