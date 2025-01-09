package autoupdater

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"time"

	"go.uber.org/zap"

	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/logfields"
)

type syncAction int

const (
	undefined syncAction = iota
	enqueue
	unlabel
	resetStatusState
)

// InitSync does an initial synchronization of the autoupdater queues with the
// pull request state at GitHub.
// It must be run **before** Autoupdater is started.
// Pull request information is queried from github.
// If a PR meets a condition to be enqueued for auto-updates it is enqueued.
// If it meets a condition for not being automatically updated, it is dequeued.
// If a PR has the [a.HeadLabel] it is removed, if it has a non-pending github
// status from directorius it is set to pending.
func (a *Autoupdater) InitSync(ctx context.Context) error {
	for repo := range a.MonitoredRepositories {
		err := a.sync(ctx, repo.OwnerLogin, repo.RepositoryName)
		if err != nil {
			return fmt.Errorf("syncing %s failed: %w", repo, err)
		}
	}

	return nil
}

func (a *Autoupdater) sync(ctx context.Context, owner, repo string) error {
	fields := []zap.Field{logfields.Repository(repo), logfields.RepositoryOwner(owner)}
	logger := a.Logger.With(fields...)

	logger.Info("starting synchronization")

	it := a.GitHubClient.ListPRs(ctx, owner, repo)
	next, stop := iter.Pull2(it)
	defer stop()

	stats := syncStat{StartTime: time.Now()}
	hasMore := true
	for {
		var ghPR *githubclt.PR

		err := a.Retryer.Run(ctx, func(context.Context) (err error) {
			ghPR, err, hasMore = next()
			return err
		}, logfields.NewWith(fields, logfields.Operation("sync")))
		if err != nil {
			stop()
			return err
		}
		if !hasMore {
			break
		}

		stats.Seen++

		actions := a.evaluateActions(ghPR)
		if len(actions) > 0 {
			stats.PRsOutOfSync++
		}

		for _, action := range actions {
			pr, err := NewPullRequest(ghPR.Number, ghPR.Branch, ghPR.Author, ghPR.Title, ghPR.Link)
			if err != nil {
				return fmt.Errorf("pr information retrieved from github is incomplete: %w", err)
			}

			logger = logger.With(pr.LogFields...)

			switch action {
			case unlabel:
				err := a.removeLabel(ctx, owner, repo, pr)
				if err != nil {
					logger.Warn(
						"removing pull request label failed",
						zap.Error(err),
					)
					continue
				}
				stats.Unlabeled++

			case enqueue:
				err := a.enqueuePR(ctx, owner, repo, ghPR.BaseBranch, pr)
				if errors.Is(err, ErrAlreadyExists) {
					logger.Debug("queue in-sync, pr is already enqueued")
					break
				}
				if err != nil {
					stats.Failures++
					logger.Warn("adding pr to queue failed",
						zap.Error(err),
					)
					break
				}

				stats.Enqueued++
				logger.Info("enqueued pr")

			case resetStatusState:
				err := createCommitStatus(ctx,
					a.GitHubClient, logger, a.Retryer,
					owner, repo, pr, ghPR.HeadCommit,
					githubclt.StatePending,
				)
				if err != nil {
					logger.Warn("setting github status state failed", zap.Error(err))
					continue
				}

				stats.StatusStateReset++

			default:
				logger.DPanic(
					"evaluateActions() returned unexpected value",
					zap.Int("value", int(action)),
				)
			}
		}
	}

	stats.EndTime = time.Now()

	logger.Info("synchronization finished",
		stats.LogFields()...,
	)

	return nil
}

func (a *Autoupdater) enqueuePR(ctx context.Context, repoOwner, repo, baseBranch string, pr *PullRequest) error {
	bb, err := NewBaseBranch(repoOwner, repo, baseBranch)
	if err != nil {
		return fmt.Errorf("incomplete base branch information: %w", err)
	}

	return a.Enqueue(ctx, bb, pr)
}

func (a *Autoupdater) removeLabel(ctx context.Context, repoOwner, repo string, pr *PullRequest) error {
	return a.Retryer.Run(ctx, func(ctx context.Context) error {
		return a.GitHubClient.RemoveLabel(ctx,
			repoOwner, repo, pr.Number,
			a.HeadLabel,
		)
	}, logfields.NewWith(pr.LogFields, logfields.Operation("github_remove_label")))
}

func (a *Autoupdater) evaluateActions(pr *githubclt.PR) []syncAction {
	var result []syncAction

	for _, label := range pr.Labels {
		switch {
		case label == a.HeadLabel:
			result = append(result, unlabel)
		case a.TriggerLabels.Contains(label):
			result = append(result, enqueue)
		}
	}

	if pr.AutoMergeEnabled && a.TriggerOnAutomerge {
		result = append(result, enqueue)
	}

	for _, status := range pr.Statuses {
		if status.Context == githubStatusContext && status.State != githubclt.StatePending {
			result = append(result, resetStatusState)
		}
	}

	return result
}
