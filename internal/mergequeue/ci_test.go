package mergequeue

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/simplesurance/directorius/internal/jenkins"
	"github.com/simplesurance/directorius/internal/mergequeue/mocks"
	"github.com/simplesurance/directorius/internal/retry"
)

func TestCIRun_IgnoresJobsWithUnknownContext(t *testing.T) {
	l := forwardLogsToTestLogger(t)
	mockctrl := gomock.NewController(t, gomock.WithOverridableExpectations())
	ciClient := mocks.NewMockJenkinsClient(mockctrl)

	ciClient.
		EXPECT().
		Build(gomock.Any(), gomock.Cond(func(j *jenkins.Job) bool {
			return j.String() == "jobs/build"
		})).
		Return(int64(1), nil).
		AnyTimes()

	b, err := jenkins.ParseBuildURL(buildURLFromQueueItemID)
	ciClient.
		EXPECT().
		GetBuildFromQueueItemID(gomock.Any(), gomock.Any()).
		Return(b, err)

	ci := CI{
		logger:  l,
		retryer: retry.NewRetryer(),
		Client:  ciClient,
		Jobs: map[string]*jenkins.JobTemplate{
			"build": {
				RelURL: "jobs/build",
			},
			"test": {
				RelURL: "jobs/test",
			},
		},
	}

	pr, err := NewPullRequest(prNR, prBranch, "me", "test pr", "")
	require.NoError(t, err)

	err = ci.Run(context.Background(), pr, "build", "check")
	require.NoError(t, err)
}
