package autoupdater

import (
	"context"

	"github.com/simplesurance/directorius/internal/jenkins"
	"github.com/simplesurance/directorius/internal/logfields"

	"go.uber.org/zap"
)

type DryCIClient struct {
	logger *zap.Logger
}

func NewDryCIClient(logger *zap.Logger) *DryCIClient {
	return &DryCIClient{
		logger: logger.Named("dry_ci_client"),
	}
}

func (c *DryCIClient) Build(_ context.Context, job *jenkins.Job) (int64, error) {
	c.logger.Info("simulated triggering of ci job", logfields.CIJob(job.String()))
	return -1, nil
}

func (c *DryCIClient) String() string {
	return "N/A - dry run enabled"
}
