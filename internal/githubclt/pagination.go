package githubclt

import "github.com/shurcooL/githubv4"

type PageInfo struct {
	EndCursor   githubv4.String
	HasNextPage bool
}
