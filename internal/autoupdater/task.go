package autoupdater

type Task uint8

const (
	TaskNone Task = iota
	TaskTriggerCI
)
