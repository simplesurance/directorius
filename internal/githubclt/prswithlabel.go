package githubclt

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/shurcooL/githubv4"
)

type Status struct {
	Context string
	State   string
}

type PRsWithLabelResult struct {
	Number           int
	Statuses         []*Status
	Labels           []string
	AutoMergeEnabled bool
}

type prsWithLabelQuery struct {
	Search struct {
		PageInfo PageInfo
		Nodes    []struct {
			PullRequest struct {
				Number int
				Labels struct {
					Nodes []struct {
						Name string
					}
				} `graphql:"labels(first: 100)"`
				AutoMergeRequest struct {
					EnabledAt githubv4.DateTime
				}
				HeadRef struct {
					Target struct {
						Commit struct {
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
	} `graphql:"search(query: $query, type: ISSUE, first: 100, after: $pullRequestCursor)"`
}

func (clt *Client) PRsWithLabel(ctx context.Context, owner, repo string, labels []string) iter.Seq2[*PRsWithLabelResult, error] {
	lbs := make([]string, 0, len(labels))
	for _, l := range labels {
		lbs = append(lbs, "\""+l+"\"")
	}
	vars := map[string]any{
		"pullRequestCursor": (*githubv4.String)(nil),
		"query":             githubv4.String(fmt.Sprintf("repo:%s/%s is:pr is:open label:%s", owner, repo, strings.Join(lbs, ","))),
	}

	var prs []*PRsWithLabelResult
	hasNext := true

	return func(yield func(pr *PRsWithLabelResult, err error) bool) {
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

			var q prsWithLabelQuery
			err := clt.graphQLClt.Query(ctx, &q, vars)
			if err != nil {
				yield(nil, err)
				return
			}

			hasNext = q.Search.PageInfo.HasNextPage
			vars["pullRequestCursor"] = q.Search.PageInfo.EndCursor

			prs = prsWithLabelQueryToPrsWithLabelResult(&q)
			fmt.Printf("len: %d\n", len(prs)) // FIXME
		}
	}
}

func prsWithLabelQueryToPrsWithLabelResult(q *prsWithLabelQuery) []*PRsWithLabelResult {
	result := make([]*PRsWithLabelResult, 0, len(q.Search.Nodes))

	for _, node := range q.Search.Nodes {
		pr := node.PullRequest
		prR := PRsWithLabelResult{
			Number:           pr.Number,
			AutoMergeEnabled: !pr.AutoMergeRequest.EnabledAt.IsZero(),
		}
		prR.Statuses = make([]*Status, 0, len(pr.HeadRef.Target.Commit.Status.Contexts))
		for _, context := range pr.HeadRef.Target.Commit.Status.Contexts {
			prR.Statuses = append(prR.Statuses, &Status{
				Context: context.Context,
				State:   context.State,
			})
		}

		prR.Labels = make([]string, 0, len(pr.Labels.Nodes))
		for _, label := range pr.Labels.Nodes {
			prR.Labels = append(prR.Labels, label.Name)
		}
		result = append(result, &prR)

	}

	return result
}
