package jenkins

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
