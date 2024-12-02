package jenkins

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplate(t *testing.T) {
	jt := JobTemplate{
		RelURL: "job/{{ pathescape .Branch }}/{{ .PullRequestNumber }}/build?branch={{ queryescape .Branch }}",
		Parameters: map[string]string{
			"Branch": "{{ .Branch }}",
			"PRNr":   "{{ .PullRequestNumber }}",
		},
	}

	d := TemplateData{
		PullRequestNumber: "456",
		Branch:            "ma/i-n br",
	}

	j, err := jt.Template(d)
	require.NoError(t, err)

	var params map[string]string

	err = json.Unmarshal(j.parametersJSON, &params)
	require.NoError(t, err)

	assert.Contains(t, params, "Branch")
	assert.Equal(t, d.Branch, params["Branch"])
	assert.Contains(t, params, "PRNr")
	assert.Equal(t, d.PullRequestNumber, params["PRNr"])

	assert.Equal(t, "job/ma%2Fi-n%20br/456/build?branch=ma%2Fi-n+br", j.relURL)
}

func TestTemplateFailsOnUndefinedKey(t *testing.T) {
	jt := JobTemplate{
		RelURL: "abc",
		Parameters: map[string]string{
			"UndefinedK": "{{ .Undefined }}",
		},
	}

	d := TemplateData{}

	_, err := jt.Template(d)
	require.Error(t, err)
}
