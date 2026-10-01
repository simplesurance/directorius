package mergequeue

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/simplesurance/directorius/internal/mergequeue/pages/pagestypes"
)

func TestListTemplateRendersAuthorAndAssignees(t *testing.T) {
	h := NewHTTPService(&Coordinator{Config: &Config{Logger: zaptest.NewLogger(t)}}, "/")

	pr := func(nr int, author string, assignees ...string) *pagestypes.PullRequest {
		return &pagestypes.PullRequest{
			Number:    "1",
			Priority:  pagestypes.PRPriorityOptions(nr, 0),
			Link:      &pagestypes.Link{Text: "<b>title</b>", URL: "https://example.com/pr"},
			Author:    pagestypes.NewPerson(author),
			Assignees: pagestypes.NewPersons(assignees),
			Status:    pagestypes.PRStatus(nr == 1),
		}
	}

	data := &pagestypes.ListData{
		CreatedAt: time.Now(),
		Queues: []*pagestypes.Queue{
			{
				RepositoryOwner: "owner",
				Repository:      "repo",
				BaseBranch:      "main",
				ActivePRs:       []*pagestypes.PullRequest{pr(1, "some-bot", "alice", "bob")},
				SuspendedPRs:    []*pagestypes.PullRequest{pr(2, "", "")},
			},
			{
				RepositoryOwner: "owner",
				Repository:      "repo",
				BaseBranch:      "develop",
				Paused:          true,
				ActivePRs:       []*pagestypes.PullRequest{pr(3, "carol")},
			},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, h.templates.ExecuteTemplate(&buf, "list.html.tmpl", data))
	out := buf.String()

	require.Contains(t, out, ">Assignee<")
	require.Contains(t, out, "https://github.com/some-bot")
	require.Contains(t, out, ">alice<")
	require.Contains(t, out, ">bob<")
	require.Contains(t, out, "&lt;b&gt;title&lt;/b&gt;", "title must be escaped")
	require.Contains(t, out, `id="priority_form_0"`)
	require.Contains(t, out, `id="priority_form_1"`)
	require.Contains(t, out, `form="priority_form_1"`)
	require.Contains(t, out, "pill-suspended")
	require.Contains(t, out, "paused-banner")
	require.Contains(t, out, `<span class="pill">paused</span>`)
	require.NotContains(t, out, "ZgotmplZ", "a value was rejected by html/template escaping")
}
