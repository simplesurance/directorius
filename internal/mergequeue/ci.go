package mergequeue

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/simplesurance/directorius/internal/jenkins"
	"github.com/simplesurance/directorius/internal/logfields"
	"github.com/simplesurance/directorius/internal/retry"

	"go.uber.org/zap"
)

// Run starts a build for CI jobs that have as context ciContext.
// [pr.LastStartedCIBuilds] is overwritten with the URLs of the started builds.
func (c *CI) Run(ctx context.Context, pr *PullRequest, ciContext ...string) error {
	var errs []error

	ch := make(chan *runCiResult, len(ciContext))
	var started int

	for _, ciContext := range ciContext {
		jobTempl, exists := c.Jobs[strings.ToLower(ciContext)]
		if !exists {
			c.logger.Debug(fmt.Sprintf("skipping triggering ci job, a job with the github context %q is not configured", ciContext),
				pr.LogFields...)

			continue
		}
		go c.runCIJobToCh(ctx, ch, c.retryer, pr, jobTempl)
		started++
	}

	builds := make(map[string]*jenkins.Build, started)

	for range started {
		result := <-ch
		if result.Err != nil {
			errs = append(errs, result.Err)
			continue
		}
		builds[result.Build.JobName] = result.Build
	}
	pr.SetLastStartedCIBuilds(builds)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

type runCiResult struct {
	Err   error
	Build *jenkins.Build
}

// runCIJobToCh calls [CI.runCIJob] and sends the result to resultCh
func (c *CI) runCIJobToCh(ctx context.Context, resultCh chan<- *runCiResult, retryer *retry.Retryer, pr *PullRequest, jobTempl *jenkins.JobTemplate) {
	build, err := c.runCIJob(ctx, retryer, pr, jobTempl)
	if err != nil {
		err = fmt.Errorf("%s: %w", jobTempl.RelURL, err)
	}

	resultCh <- &runCiResult{
		Err:   err,
		Build: build,
	}
}

func (c *CI) runCIJob(ctx context.Context, retryer *retry.Retryer, pr *PullRequest, jobTempl *jenkins.JobTemplate) (*jenkins.Build, error) {
	var queuedBuildItemID int64
	var build *jenkins.Build

	ctx, cancelFN := context.WithCancel(ctx)
	defer cancelFN()

	timer := time.AfterFunc(operationTimeout, cancelFN)

	job, err := jobTempl.Template(jenkins.TemplateData{
		PullRequestNumber: strconv.Itoa(pr.Number),
		Branch:            pr.Branch,
	})
	if err != nil {
		return nil, fmt.Errorf("templating jenkins job failed: %w", err)
	}

	lf := logfields.NewWith(pr.LogFields, logfields.CIJob(job.String()))

	err = retryer.Run(
		ctx,
		func(ctx context.Context) (err error) {
			queuedBuildItemID, err = c.Client.Build(ctx, job)
			return err
		},
		logfields.NewWith(lf, logfields.Operation("ci.run_job")),
	)
	if err != nil {
		return nil, fmt.Errorf("triggering ci build via url %q failed: %w", job, err)
	}

	timer.Stop()

	c.logger.Debug("triggered ci job",
		logfields.NewWith(lf, zap.Int64("ci.queued_item.id", queuedBuildItemID))...,
	)

	timer = time.AfterFunc(operationTimeout, cancelFN)
	err = retryer.Run(
		ctx,
		func(ctx context.Context) (err error) {
			build, err = c.Client.GetBuildFromQueueItemID(ctx, queuedBuildItemID)
			return err
		},
		logfields.NewWith(lf, logfields.Operation("ci.get_build_url")),
	)
	if err != nil {
		return nil, fmt.Errorf("retrieving build information for queue item id %d failed: %w", queuedBuildItemID, err)
	}

	timer.Stop()

	c.logger.Debug("retrieved url of queued ci build",
		logfields.NewWith(lf, zap.Stringer("ci.build", build))...,
	)

	return build, nil
}
