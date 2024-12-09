package autoupdater

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
		ctx, cancelFN := context.WithTimeout(ctx, operationTimeout)
		defer cancelFN()

		job, err := jobTempl.Template(jenkins.TemplateData{
			PullRequestNumber: strconv.Itoa(pr.Number),
			Branch:            pr.Branch,
		})
		if err != nil {
			return fmt.Errorf("templating jenkins job %q failed: %w", jobTempl.RelURL, err)
		}

		logfields := append(
			[]zap.Field{
				logfields.Operation("triggering_ci_job"),
				logfields.CIJob(job.String()),
			},
			pr.LogFields...,
		)

		err = retryer.Run(
			ctx,
			func(ctx context.Context) error { return c.Client.Build(ctx, job) },
			logfields,
		)
		if err != nil {
			return fmt.Errorf("running ci job %q failed: %w", job, err)
		}

		c.logger.Debug("triggered ci job", logfields...)
		cancelFN()
	}

	return nil
}
