package jenkins

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate(t *testing.T) {
	jt := JobTemplate{
		RelURL: "job/{{ pathescape .Branch }}/{{ .Commit }}/build?branch={{ queryescape .Branch }}",
		PostData: `
Branch: {{ .Branch }},
Commit: {{ .Commit }},
PRNr: {{ .PullRequestNumber }}
`,
	}

	d := templateData{
		Commit:            "123",
		PullRequestNumber: "456",
		Branch:            "ma/i-n br",
	}

	j, err := jt.Template(d)
	require.NoError(t, err)

	expectedPostData := `
Branch: ma/i-n br,
Commit: 123,
PRNr: 456
`
	assert.Equal(t, expectedPostData, string(j.postData))

	assert.Equal(t, "job/ma%2Fi-n%20br/123/build?branch=ma%2Fi-n+br", j.relURL)
}
