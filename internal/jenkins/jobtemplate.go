package jenkins

import (
	"bytes"
	"fmt"
	"net/url"
	"text/template"
)

var templateFuncs = template.FuncMap{
	"queryescape": url.QueryEscape,
	"pathescape":  url.PathEscape,
}

// JobTemplate is a Job definition that can contain Go-Template statements.
type JobTemplate struct {
	RelURL   string
	PostData string
}

type templateData struct {
	Commit            string
	PullRequestNumber string
	Branch            string
}

// Template creates a concrete [Job] from j by templating it with
// [templateData] and [templateFuncs.
func (j *JobTemplate) Template(data templateData) (*Job, error) {
	var postDataTemplated bytes.Buffer
	var relURLTemplated bytes.Buffer

	templ, err := template.New("job_templ").Funcs(templateFuncs).Parse(j.PostData)
	if err != nil {
		return nil, fmt.Errorf("parsing post_data as template failed: %w", err)
	}

	if err := templ.Execute(&postDataTemplated, data); err != nil {
		return nil, fmt.Errorf("templating post_data failed: %w", err)
	}

	templ, err = template.New("job_templ").Funcs(templateFuncs).Parse(j.RelURL)
	if err != nil {
		return nil, fmt.Errorf("parsing endpoint as template failed: %w", err)
	}

	if err := templ.Execute(&relURLTemplated, data); err != nil {
		return nil, fmt.Errorf("templating post_data failed: %w", err)
	}

	return &Job{
		relURL:   relURLTemplated.String(),
		postData: postDataTemplated.Bytes(),
	}, nil
}
