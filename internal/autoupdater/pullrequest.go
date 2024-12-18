package autoupdater

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/google/go-github/v67/github"

	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/jenkins"
	"github.com/simplesurance/directorius/internal/logfields"
)

type PullRequest struct {
	Number    int
	Branch    string
	Author    string
	Title     string
	Link      string
	LogFields []zap.Field

	// InActiveQueueSince is the when the PR has been added to the active
	// queue. When The PR is suspended and resumed it is reset.
	InActiveQueueSince time.Time
	// EnqueuedAt is the time when the PR is added to merge-queue
	EnqueuedAt time.Time

	// LastStartedCIBuilds keys are [jenkins.Build.jobName]
	LastStartedCIBuilds map[string]*jenkins.Build

	Priority int8

	// SuspendCount is increased whenever a PR is moved from the active to
	// the suspend queue.
	SuspendCount atomic.Uint32

	stateUnchangedSince time.Time
	lock                sync.Mutex // must be held when accessing stateUnchangedSince
}

func NewPullRequestFromEvent(ev *github.PullRequest) (*PullRequest, error) {
	if ev == nil {
		return nil, errors.New("github pull request event is nil")
	}

	return NewPullRequest(
		ev.GetNumber(),
		ev.GetHead().GetRef(),
		ev.GetUser().GetLogin(),
		ev.GetTitle(),
		ev.GetLinks().GetHTML().GetHRef(),
	)
}

func NewPullRequest(nr int, branch, author, title, link string) (*PullRequest, error) {
	if nr <= 0 {
		return nil, fmt.Errorf("number is %d, must be >0", nr)
	}

	if branch == "" {
		return nil, errors.New("branch is empty")
	}

	return &PullRequest{
		Number: nr,
		Branch: branch,
		Author: author,
		Title:  title,
		Link:   link,
		LogFields: []zap.Field{
			logfields.PullRequest(nr),
			logfields.Branch(branch),
		},
		LastStartedCIBuilds: map[string]*jenkins.Build{},
		EnqueuedAt:          time.Now(),
	}, nil
}

// Equal returns true if p and other are of type PullRequest and its Number
// field contains the same value.
func (p *PullRequest) Equal(other *PullRequest) bool {
	return p.Number == other.Number
}

func (p *PullRequest) GetStateUnchangedSince() time.Time {
	p.lock.Lock()
	defer p.lock.Unlock()
	return p.stateUnchangedSince
}

func (p *PullRequest) SetStateUnchangedSinceIfNewer(t time.Time) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.stateUnchangedSince.Before(t) {
		p.stateUnchangedSince = t
	}
}

func (p *PullRequest) SetStateUnchangedSince(t time.Time) {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.stateUnchangedSince = t
}

func (p *PullRequest) SetStateUnchangedSinceIfZero(t time.Time) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.stateUnchangedSince.IsZero() {
		p.stateUnchangedSince = t
	}
}

// FailedCIStatusIsForNewestBuild returns true if a required and failed status
// in [statuses] is for a job in [pr.LastStartedCIBuilds] and has the same or
// newer build id.
//
// This function is not concurrency-safe, reading and writing to
// [pr.CITriggerStatus.BuildURLs] must be serialized by for example only
// running both operations as part of [queue.updatePR].
func (p *PullRequest) FailedCIStatusIsForNewestBuild(logger *zap.Logger, statuses []*githubclt.CIJobStatus) (bool, error) {
	if len(statuses) == 0 {
		return false, errors.New("github status ci job list is empty")
	}

	for _, status := range statuses {
		if !status.Required || status.Status != githubclt.CIStatusFailure {
			continue
		}

		build, err := jenkins.ParseBuildURL(status.JobURL)
		if err != nil {
			logger.Warn(
				"parsing ci job url from github webhook as jenkins build url failed,",
				logfields.CIBuildURL(build.String()),
				zap.Error(err),
			)
			return false, fmt.Errorf("parsing ci job url (%q) from github webhook as jenkins build url failed: %w", status.JobURL, err)
		}

		lastBuild, exists := p.LastStartedCIBuilds[build.JobName]
		if !exists {
			continue
		}

		if build.Number >= lastBuild.Number {
			logger.Debug("failed ci job status is for latest or newer build",
				logfields.CIJob(build.JobName),
				zap.Stringer("ci.latest_build", lastBuild),
				zap.Stringer("github.ci_failed_status_build", build),
			)
			return true, nil
		}
	}

	return false, nil
}
