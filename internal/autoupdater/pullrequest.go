package autoupdater

import (
	"errors"
	"fmt"
	"maps"
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

	// inActiveQueueSince is the when the PR has been added to the active
	// queue. When The PR is suspended and resumed it is reset.
	inActiveQueueSince atomic.Pointer[time.Time]
	// EnqueuedAt is the time when the PR is added to merge-queue
	EnqueuedAt time.Time

	Priority atomic.Int32

	// SuspendCount is increased whenever a PR is moved from the active to
	// the suspend queue.
	SuspendCount atomic.Uint32

	// lastStartedCIBuilds keys are [jenkins.Build.jobName]
	lastStartedCIBuilds map[string]*jenkins.Build
	stateUnchangedSince time.Time
	lock                sync.Mutex // must be held when accessing stateUnchangedSince, lastStartedCIBuilds

	GithubStatusLastSetState ReportedStatusState
	GithubStatusLock         sync.Mutex
}

type ReportedStatusState struct {
	Commit string
	State  githubclt.StatusState
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

	pr := PullRequest{
		Number: nr,
		Branch: branch,
		Author: author,
		Title:  title,
		Link:   link,
		LogFields: []zap.Field{
			logfields.PullRequest(nr),
			logfields.Branch(branch),
		},
		lastStartedCIBuilds: map[string]*jenkins.Build{},
		EnqueuedAt:          time.Now(),
		inActiveQueueSince:  atomic.Pointer[time.Time]{},
	}
	pr.inActiveQueueSince.Store(&time.Time{})

	return &pr, nil
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

// SetInActiveQueueSince sets the inActiveQueueSince to [time.Now()].
func (p *PullRequest) SetInActiveQueueSince() {
	n := time.Now()
	p.inActiveQueueSince.Store(&n)
}

// InActiveQueueSince returns since when the PR has been in the active queue.
// If it isn't in the active queue the zero value is returned.
func (p *PullRequest) InActiveQueueSince() time.Time {
	return *p.inActiveQueueSince.Load()
}

func (p *PullRequest) SetLastStartedCIBuilds(m map[string]*jenkins.Build) {
	p.lock.Lock()
	p.lastStartedCIBuilds = m
	p.lock.Unlock()
}

func (p *PullRequest) GetLastStartedCIBuilds() map[string]*jenkins.Build {
	p.lock.Lock()
	defer p.lock.Unlock()
	return maps.Clone(p.lastStartedCIBuilds)
}
