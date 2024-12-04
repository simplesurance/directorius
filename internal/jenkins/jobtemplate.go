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
	RelURL     string
	Parameters map[string]string
}

type TemplateData struct {
	PullRequestNumber string
	Branch            string
}

// Template creates a concrete [Job] from j by templating it with
// [templateData] and [templateFuncs.
func (j *JobTemplate) Template(data TemplateData) (*Job, error) {
	var relURLTemplated bytes.Buffer

	templ := template.New("job_templ").Funcs(templateFuncs).Option("missingkey=error")

	templ, err := templ.Parse(j.RelURL)
	if err != nil {
		return nil, fmt.Errorf("parsing endpoint as template failed: %w", err)
	}

	if err := templ.Execute(&relURLTemplated, data); err != nil {
		return nil, fmt.Errorf("templating post_data failed: %w", err)
	}

	if len(j.Parameters) == 0 {
		return &Job{
			relURL: relURLTemplated.String(),
		}, nil
	}

	templatedParams, err := j.templateParameters(data, templ)
	if err != nil {
		return nil, err
	}

	return &Job{
		relURL:     relURLTemplated.String(),
		parameters: templatedParams,
	}, nil
}

func (j *JobTemplate) templateParameters(data TemplateData, templ *template.Template) (map[string]string, error) {
	templatedParams := make(map[string]string, len(j.Parameters))

	for k, v := range j.Parameters {
		var bufK, bufV bytes.Buffer

		templK, err := templ.Parse(k)
		if err != nil {
			return nil, fmt.Errorf("parsing parameter key %q as template failed: %w", k, err)
		}

		if err := templK.Execute(&bufK, data); err != nil {
			return nil, fmt.Errorf("templating post_data failed: %w", err)
		}

		templV, err := templ.Parse(v)
		if err != nil {
			return nil, fmt.Errorf("parsing parameter value %q of key %q as template failed: %w", v, k, err)
		}

		if err := templV.Execute(&bufV, data); err != nil {
			return nil, fmt.Errorf("templating post_data failed: %w", err)
		}

		templatedParams[bufK.String()] = bufV.String()
	}

	return templatedParams, nil
}
