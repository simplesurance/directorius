package autoupdate

import (
	"context"
	"fmt"
	"strconv"

	"github.com/simplesurance/directorius/internal/jenkins"
	"github.com/simplesurance/directorius/internal/logfields"
	"go.uber.org/zap"
)

func (c *CI) RunAll(ctx context.Context, retryer Retryer, pr *PullRequest) error {
	for _, jobTempl := range c.Jobs {
		job, err := jobTempl.Template(jenkins.TemplateData{
			PullRequestNumber: strconv.Itoa(pr.Number),
			Branch:            pr.Branch,
		})
		if err != nil {
			return fmt.Errorf("templating jenkins job %q failed: %w", jobTempl.RelURL, err)
		}

		logfields := append(
			[]zap.Field{
				logfields.Event("triggering_ci_job"),
				logfields.CIJob(job.String()),
			},
			pr.LogFields...,
		)

		err = retryer.Run(
			ctx,
			func(ctx context.Context) error { return c.Server.Build(ctx, job) },
			logfields,
		)
		if err != nil {
			return fmt.Errorf("running ci job %q failed: %w", job, err)
		}

		c.logger.Info("triggered ci job", logfields...)
	}

	return nil
}
