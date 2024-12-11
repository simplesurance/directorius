package jenkins

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

var reJenkinsBuildURLPath = regexp.MustCompile(`^/job/(\S)*/\d+`)

type Build struct {
	JobName string
	Number  int64
	url     *url.URL
}

func ParseBuildURL(buildURL string) (*Build, error) {
	u, err := normalizeBuildURL(buildURL)
	if err != nil {
		return nil, err
	}

	nrStr := path.Base(u.Path)
	nr, err := strconv.ParseInt(nrStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("converting last path element (buildnr) in url %q to an int failed: %w", nrStr, err)
	}

	jobName := strings.TrimPrefix(path.Dir(u.Path), "/job/")
	if jobName == "" {
		return nil, errors.New("extracting job name from url failed")
	}

	return &Build{
		Number:  nr,
		JobName: jobName,
		url:     u,
	}, nil
}

func (b *Build) String() string {
	return b.url.Redacted()
}

func toRequestBody(j *Job) io.Reader {
	if len(j.parameters) == 0 {
		return nil
	}

	formData := make(url.Values, len(j.parameters))
	for k, v := range j.parameters {
		formData.Set(k, v)
	}

	return strings.NewReader(formData.Encode())
}

// normalizeBuildURL removed optional parts from a Jenkins build URL, for
// example a "/display/redirect?page=tests" suffix.
func normalizeBuildURL(buildURL string) (*url.URL, error) {
	u, err := url.Parse(buildURL)
	if err != nil {
		return nil, fmt.Errorf("could not parse url: %w", err)
	}

	p := reJenkinsBuildURLPath.FindString(u.Path)
	if p == "" {
		return nil, fmt.Errorf("url path (%q) does not match the expected pattern: %s", u.Path, reJenkinsBuildURLPath.String())
	}

	u.Path = p
	u.RawQuery = ""
	u.Opaque = ""

	return u, nil
}
