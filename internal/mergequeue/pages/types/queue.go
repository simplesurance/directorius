package types

type Queue struct {
	RepositoryOwner string
	Repository      string
	BaseBranch      string

	ActivePRs    []*PullRequest
	SuspendedPRs []*PullRequest

	Paused bool
}
