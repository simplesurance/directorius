package mergequeue

import "fmt"

// Repository is a identifies uniquely a GitHub Repository.
type Repository struct {
	OwnerLogin     string
	RepositoryName string
}

func (r *Repository) String() string {
	return fmt.Sprintf("%s/%s", r.OwnerLogin, r.RepositoryName)
}
