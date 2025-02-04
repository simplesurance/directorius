package mergequeue

import "errors"

var (
	ErrAlreadyExists       = errors.New("already exist")
	ErrNotFound            = errors.New("not found")
	ErrPullRequestIsClosed = errors.New("pull request is closed")
)
