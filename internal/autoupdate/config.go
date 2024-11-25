package autoupdate

import (
	"github.com/simplesurance/directorius/internal/jenkins"
	github_prov "github.com/simplesurance/directorius/internal/provider/github"

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
	TriggerLabels         map[string]struct{}
	HeadLabel             string
	// When DryRun is enabled all GitHub API operation that could result in
	// a change will be simulated and always succeed.
	DryRun bool
}

type CI struct {
	Server *jenkins.Server
	Jobs   []*jenkins.JobTemplate
}

func (cfg *Config) setDefaults() {
	if cfg.Logger == nil {
		cfg.Logger = zap.L().Named(loggerName)
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
		if cfg.CI.Server == nil && len(cfg.CI.Jobs) > 0 {
			panic("autoupdater config: ci jobs are defined but no server")
		}
	}
}
