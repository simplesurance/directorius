package autoupdater

import "time"

const allowRetriggerCIJobsOnSameCommitAfter = 10 * time.Minute

type CIJobTriggerStatus struct {
	lastRunAt     time.Time
	lastRunCommit string
}

func (s *CIJobTriggerStatus) CIJobsTriggeredRecently(commit string) bool {
	return s.lastRunCommit == commit && time.Since(s.lastRunAt) <= allowRetriggerCIJobsOnSameCommitAfter
}
