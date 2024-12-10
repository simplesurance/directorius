package autoupdater

import (
	"time"
)

const allowRetriggerCIJobsOnSameCommitAfter = 10 * time.Minute

type CILastRun struct {
	At     time.Time
	Commit string
}

func (s *CILastRun) CIJobsTriggeredRecently(commit string) bool {
	return s.Commit == commit && time.Since(s.At) <= allowRetriggerCIJobsOnSameCommitAfter
}
