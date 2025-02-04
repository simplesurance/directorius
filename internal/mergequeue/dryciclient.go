package mergequeue

import (
	"context"
	"strconv"
	"sync/atomic"

	"github.com/simplesurance/directorius/internal/jenkins"
	"github.com/simplesurance/directorius/internal/logfields"

	"go.uber.org/zap"
)

type DryCIClient struct {
	logger    *zap.Logger
	itemIDGen atomic.Int64
}

func NewDryCIClient(logger *zap.Logger) *DryCIClient {
	return &DryCIClient{
		logger: logger.Named("dry_ci_client"),
	}
}

func (c *DryCIClient) Build(_ context.Context, job *jenkins.Job) (int64, error) {
	c.logger.Info("simulated triggering of ci job", logfields.CIJob(job.String()))
	return c.itemIDGen.Add(1), nil
}

func (c *DryCIClient) GetBuildURL(_ context.Context, id int64) (string, error) {
	c.logger.Info("simulated retrieving url of ci build")
	return "https://dryciclient.invalid/build/" + strconv.FormatInt(id, 10), nil
}

func (c *DryCIClient) String() string {
	return "N/A - dry run enabled"
}

func (c *DryCIClient) GetBuildFromQueueItemID(ctx context.Context, id int64) (*jenkins.Build, error) {
	url, err := c.GetBuildURL(ctx, id)
	if err != nil {
		return nil, err
	}
	return jenkins.ParseBuildURL(url)
}
