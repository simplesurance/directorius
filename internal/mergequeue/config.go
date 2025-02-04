package mergequeue

import (
	"context"
	"fmt"

	"github.com/simplesurance/directorius/internal/jenkins"
	github_prov "github.com/simplesurance/directorius/internal/provider/github"
	"github.com/simplesurance/directorius/internal/retry"
	"github.com/simplesurance/directorius/internal/set"

	"go.uber.org/zap"
)

const loggerName = "autoupdater"

type Config struct {
	Logger *zap.Logger

	GitHubClient GithubClient
	// CI configures Jenkins Job for that builds are scheduled.
	CI *CI
	// EventChan is a channel from that Github webhook events are received
	EventChan <-chan *github_prov.Event
	Retryer   *retry.Retryer
	// MonitoredRepositories defines the GitHub repositories for that merge
	// queues are created.
	MonitoredRepositories map[Repository]struct{}
	// TriggerOnAutomerge enables adding a pull request to the merge queue
	// when automerge in GitHub is enabled
	TriggerOnAutomerge bool
	// TriggerLabels is a set of GitHub labels that when addded to a PR,
	// cause that the PR is enqueued
	TriggerLabels set.Set[string]
	// HeadLabel is the name of a GitHub label that is added/remove to the
	// first pull request in the mergequeue.
	HeadLabel string
	// DryRun can be enabled to simulate GitHub API and Jenkins Client
	// operations that would result in a change.
	DryRun bool
}

type JenkinsClient interface {
	fmt.Stringer
	Build(context.Context, *jenkins.Job) (int64, error)
	GetBuildFromQueueItemID(context.Context, int64) (*jenkins.Build, error)
}

type CI struct {
	Client JenkinsClient
	// Jobs is a map of github-status-context -> JobTemplate.
	Jobs map[string]*jenkins.JobTemplate

	retryer *retry.Retryer
	logger  *zap.Logger
}

func (cfg *Config) setDefaults() {
	if cfg.Logger == nil {
		cfg.Logger = zap.L().Named(loggerName)
	}

	if cfg.TriggerLabels == nil {
		cfg.TriggerLabels = set.Set[string]{}
	}

	if cfg.CI == nil {
		cfg.CI = &CI{}
	}

	if cfg.CI != nil {
		cfg.CI.retryer = cfg.Retryer
		cfg.CI.logger = cfg.Logger.Named("ci")

		if cfg.CI.Jobs == nil {
			cfg.CI.Jobs = map[string]*jenkins.JobTemplate{}
		}
	}
}

func (cfg *Config) mustValidate() {
	if cfg.Logger == nil {
		panic("autoupdater config: logger is nil")
	}
	if cfg.GitHubClient == nil {
		panic("autoupdater config: githubclient is nil")
	}
	if cfg.EventChan == nil {
		panic("autoupdater config: eventChan is nil")
	}
	if cfg.Retryer == nil {
		panic("autoupdater config: retryer is nil")
	}
	if cfg.MonitoredRepositories == nil {
		panic("autoupdater config: monitoredrepositories is nil")
	}
	if cfg.TriggerLabels == nil {
		panic("autoupdater config: triggerlabels is nil")
	}

	if cfg.CI != nil {
		if cfg.CI.Client == nil && len(cfg.CI.Jobs) > 0 {
			panic("autoupdater config: ci jobs are defined but no server")
		}
	}
}
