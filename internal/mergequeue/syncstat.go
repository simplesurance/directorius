package mergequeue

import (
	"time"

	"go.uber.org/zap"
)

type syncStat struct {
	StartTime        time.Time
	EndTime          time.Time
	Seen             uint
	Enqueued         uint
	Unlabeled        uint
	Failures         uint
	StatusStateReset uint
	PRsOutOfSync     uint
}

func (s *syncStat) LogFields() []zap.Field {
	return []zap.Field{
		zap.Duration("sync_duration", s.EndTime.Sub(s.StartTime)),
		zap.Uint("pr_sync.seen", s.Seen),
		zap.Uint("pr_sync.failures", s.Failures),
		zap.Uint("pr_sync.enqueued", s.Enqueued),
		zap.Uint("pr_sync.unlabeled", s.Unlabeled),
		zap.Uint("pr_sync.status_state_reset", s.StatusStateReset),
		zap.Uint("pr_sync.out_of_sync", s.PRsOutOfSync),
	}
}
