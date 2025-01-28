package githubclt

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/shurcooL/githubv4"
)

type Status struct {
	Context string
	State   StatusState
}

type PR struct {
	Number           int
	Branch           string
	BaseBranch       string
	HeadCommit       string
	Author           string
	Title            string
	Link             string
	Statuses         []*Status
	Labels           []string
	AutoMergeEnabled bool
}

type listPRsQuery struct {
	Search struct {
		PageInfo PageInfo
		Nodes    []struct {
			PullRequest struct {
				HeadRefName string
				BaseRefName string
				Title       string
				Permalink   string
				Author      struct {
					Login string
				}
				Number int
				Labels struct {
					PageInfo PageInfo
					Nodes    []struct {
						Name string
					}
				} `graphql:"labels(first: 100)"`
				AutoMergeRequest struct {
					EnabledAt time.Time
				}
				HeadRef struct {
					Target struct {
						Commit struct {
							Oid    string
							Status struct {
								Contexts []struct {
									Context string
									State   string
								}
							}
						} `graphql:"... on Commit"`
					}
				}
			} `graphql:"... on PullRequest"`
		}
	} `graphql:"search(query:$query, type:ISSUE, first:100, after:$pullRequestCursor)"`
}

// ListPRs returns an iterator over all open non-draft pull requests of the
// repository.
// If an error happens when querying the GitHub GraphQL API, it is passed as
// second argument to the iterator.
func (clt *Client) ListPRs(ctx context.Context, owner, repo string) iter.Seq2[*PR, error] {
	vars := map[string]any{
		"pullRequestCursor": (*githubv4.String)(nil),
		"query":             githubv4.String(fmt.Sprintf("repo:%s/%s is:pr is:open draft:false sort:updated-asc", owner, repo)),
	}

	var prs []*PR
	hasNext := true

	return func(yield func(pr *PR, err error) bool) {
		for {
			if len(prs) > 0 {
				if !yield(prs[0], nil) {
					return
				}

				prs = prs[1:]
				continue
			}

			if !hasNext {
				return
			}

			var q listPRsQuery
			err := clt.graphQLClt.Query(ctx, &q, vars)
			if err != nil {
				if !yield(nil, clt.wrapRetryableErrors(err)) {
					return
				}
				continue
			}

			hasNext = q.Search.PageInfo.HasNextPage
			vars["pullRequestCursor"] = q.Search.PageInfo.EndCursor

			prs = clt.prsWithLabelQueryToPrsWithLabelResult(&q)
		}
	}
}

func (clt *Client) prsWithLabelQueryToPrsWithLabelResult(q *listPRsQuery) []*PR {
	result := make([]*PR, 0, len(q.Search.Nodes))

	for _, node := range q.Search.Nodes {
		pr := node.PullRequest
		prR := PR{
			Number:           pr.Number,
			Branch:           pr.HeadRefName,
			BaseBranch:       pr.BaseRefName,
			Author:           pr.Author.Login,
			Title:            pr.Title,
			Link:             pr.Permalink,
			AutoMergeEnabled: !pr.AutoMergeRequest.EnabledAt.IsZero(),
			HeadCommit:       pr.HeadRef.Target.Commit.Oid,
		}
		prR.Statuses = make([]*Status, 0, len(pr.HeadRef.Target.Commit.Status.Contexts))
		for _, context := range pr.HeadRef.Target.Commit.Status.Contexts {
			prR.Statuses = append(prR.Statuses, &Status{
				Context: context.Context,
				// graphql returns states in all uppercase, the
				// REST API uses lowercase states
				State: StatusState(strings.ToLower(context.State)),
			})
		}

		prR.Labels = make([]string, 0, len(pr.Labels.Nodes))
		for _, label := range pr.Labels.Nodes {
			prR.Labels = append(prR.Labels, label.Name)
		}
		result = append(result, &prR)

		if pr.Labels.PageInfo.HasNextPage {
			clt.logger.Warn("incomplete pr labels retrieved, more label pages are available but only the first page was feteched, pagination is not implemented")
		}

	}

	return result
}
