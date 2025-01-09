package autoupdater

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/go-github/v67/github"
	"go.uber.org/zap"

	"github.com/simplesurance/directorius/internal/logfields"
)

type syncAction int

const (
	undefined syncAction = iota
	enqueue
	dequeue
	unlabel
)

// InitSync does an initial synchronization of the autoupdater queues with the
// pull request state at GitHub.
// This is intended to be run before Autoupdater is started.
// Pull request information is queried from github.
// If a PR meets a condition to be enqueued for auto-updates it is enqueued.
// If it meets a condition for not being automatically updated, it is dequeued.
// If a PR has the [a.headLabel] set it is removed.
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
	stats := syncStat{StartTime: time.Now()}

	logger := a.Logger.With(
		logfields.Repository(repo),
		logfields.RepositoryOwner(owner),
	)

	logger.Info("starting synchronization")

	// TODO: could we query less pull requests by ignoring PRs that are
	// closed and were last changed before goordinator started?
	it := a.GitHubClient.ListPullRequests(ctx, owner, repo, "open", "asc", "created")
	for {
		var pr *github.PullRequest

		// TODO: use a lower timeout for the retries, otherwise we might get stuck here for too long on startup
		err := a.Retryer.Run(ctx, func(context.Context) error {
			var err error
			pr, err = it.Next()
			return err
		}, nil)
		if err != nil {
			return err
		}

		if pr == nil { // iteration finished, no more results
			break
		}

		stats.Seen++

		// redefine variable, to make PR fields scoped to this iteration
		logger := logger.With(logfields.PullRequest(pr.GetNumber()))

		for _, action := range a.evaluateActions(pr) {
			switch action {
			case unlabel:
				err := a.removeLabel(ctx, owner, repo, pr)
				if err != nil {
					logger.Warn(
						"removing pull request label failed",
						zap.Error(err),
					)
				}

			case enqueue:
				err := a.enqueuePR(ctx, owner, repo, pr)
				if errors.Is(err, ErrAlreadyExists) {
					logger.Debug("queue in-sync, pr is enqueued")
					break
				}
				if err != nil {
					stats.Failures++
					logger.Warn(
						"adding pr to queue failed",
						zap.Error(err),
					)
					break
				}

				stats.Enqueued++
				logger.Info("queue was out of sync, enqueued pr")

			case dequeue:
				err := a.dequeuePR(ctx, owner, repo, pr)
				if errors.Is(err, ErrNotFound) {
					logger.Debug("queue in-sync, pr not queued",
						zap.Error(err),
					)
					break
				}

				if err != nil {
					stats.Failures++
					logger.Warn("dequeing pull request failed",
						zap.Error(err),
					)
					break
				}

				stats.Dequeued++
				logger.Info("queue was out of sync, pr dequeued")

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

func (a *Autoupdater) enqueuePR(ctx context.Context, repoOwner, repo string, ghPr *github.PullRequest) error {
	bb, err := NewBaseBranch(repoOwner, repo, ghPr.GetBase().GetRef())
	if err != nil {
		return fmt.Errorf("incomplete base branch information: %w", err)
	}

	pr, err := NewPullRequest(ghPr.GetNumber(), ghPr.GetHead().GetRef(), ghPr.GetUser().GetLogin(), ghPr.GetTitle(), ghPr.GetLinks().GetHTML().GetHRef())
	if err != nil {
		return fmt.Errorf("incomplete pr information: %w", err)
	}

	return a.Enqueue(ctx, bb, pr)
}

func (a *Autoupdater) dequeuePR(ctx context.Context, repoOwner, repo string, ghPr *github.PullRequest) error {
	bb, err := NewBaseBranch(repoOwner, repo, ghPr.GetBase().GetRef())
	if err != nil {
		return fmt.Errorf("incomplete base branch information: %w", err)
	}

	prNumber := ghPr.GetNumber()
	if prNumber <= 0 {
		return fmt.Errorf("invalid pr number: %d", prNumber)
	}

	_, err = a.Dequeue(ctx, bb, prNumber)
	return err
}

func (a *Autoupdater) removeLabel(ctx context.Context, repoOwner, repo string, ghPr *github.PullRequest) error {
	pr, err := NewPullRequestFromEvent(ghPr)
	if err != nil {
		return err
	}

	return a.Retryer.Run(ctx, func(ctx context.Context) error {
		return a.GitHubClient.RemoveLabel(ctx,
			repoOwner, repo, pr.Number,
			a.HeadLabel,
		)
	}, append(pr.LogFields, logfields.Operation("github_remove_label")))
}

func (a *Autoupdater) evaluateActions(pr *github.PullRequest) []syncAction {
	var result []syncAction

	for _, label := range pr.Labels {
		if label.GetName() == a.HeadLabel {
			result = append(result, unlabel)
		}
	}

	if pr.GetState() == "closed" {
		return append(result, dequeue)
	}

	if a.TriggerOnAutomerge && pr.GetAutoMerge() != nil {
		return append(result, enqueue)
	}

	if len(a.TriggerLabels) != 0 {
		for _, label := range pr.Labels {
			labelName := label.GetName()
			if _, exist := a.TriggerLabels[labelName]; exist {
				return append(result, enqueue)
			}
		}

		return append(result, dequeue)
	}

	return nil
}
