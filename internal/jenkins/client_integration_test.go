package jenkins_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"github.com/simplesurance/directorius/internal/jenkins"
	"github.com/simplesurance/directorius/internal/retry"
)

const envIntegrationTestsEnabled = "DIRECTORIUS_RUN_INTEGRATION_TESTS"

func TestBuildAndGetBuildURL(t *testing.T) {
	if os.Getenv(envIntegrationTestsEnabled) == "" {
		t.Skip(envIntegrationTestsEnabled, "environment variable is empty")
	}

	logger := zaptest.NewLogger(t)
	t.Cleanup(zap.ReplaceGlobals(logger))

	url := os.Getenv("JENKINS_URL")
	user := os.Getenv("JENKINS_USER")
	passwd := os.Getenv("JENKINS_PASSWD")

	clt, err := jenkins.NewClient(logger, url, user, passwd)
	require.NoError(t, err)

	jt1 := jenkins.JobTemplate{
		RelURL: "job/sisu-ci-ng/job/master/",
	}

	jt2 := jenkins.JobTemplate{
		RelURL: "job/sisu-build",
		Parameters: map[string]string{
			"version": "master",
		},
	}

	job1, err := jt1.Template(jenkins.TemplateData{})
	require.NoError(t, err)
	job2, err := jt2.Template(jenkins.TemplateData{})
	require.NoError(t, err)

	qID1, err := clt.Build(context.Background(), job1)
	require.NoError(t, err)

	retryer := retry.NewRetryer()
	t.Cleanup(retryer.Stop)

	err = retryer.Run(context.Background(), func(context.Context) error {
		buildURL, err := clt.GetBuildURL(context.Background(), qID1)
		if err != nil {
			return err
		}
		logger.Sugar().Infof("got build url %q for job 1", buildURL)
		return nil
	}, nil)
	require.NoError(t, err)

	qID2, err := clt.Build(context.Background(), job2)
	require.NoError(t, err)

	err = retryer.Run(context.Background(), func(context.Context) error {
		buildURL, err := clt.GetBuildURL(context.Background(), qID2)
		if err != nil {
			return err
		}
		logger.Sugar().Infof("got build url %q for job 1", buildURL)
		return nil
	}, nil)
	require.NoError(t, err)
}
