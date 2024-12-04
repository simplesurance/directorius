package autoupdater

import (
	"context"
	"fmt"

	"github.com/simplesurance/directorius/internal/jenkins"
	github_prov "github.com/simplesurance/directorius/internal/provider/github"
	"github.com/simplesurance/directorius/internal/set"

	"go.uber.org/zap"
)

const loggerName = "autoupdater"

type Config struct {
	Logger *zap.Logger

	GitHubClient          GithubClient
	CI                    *CI
	EventChan             <-chan *github_prov.Event
	Retryer               Retryer
	MonitoredRepositories map[Repository]struct{}
	TriggerOnAutomerge    bool
	TriggerLabels         set.Set[string]
	HeadLabel             string
	// When DryRun is enabled all GitHub API operation that could result in
	// a change will be simulated and always succeed.
	DryRun bool
}

type CIClient interface {
	fmt.Stringer
	Build(context.Context, *jenkins.Job) error
}

type CI struct {
	Client CIClient
	Jobs   []*jenkins.JobTemplate

	retryer Retryer
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
