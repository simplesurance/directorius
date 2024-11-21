package cfg

import (
	"io"

	"github.com/pelletier/go-toml"
)

type Config struct {
	HTTPListenAddr  string `toml:"http_server_listen_addr"`
	HTTPSListenAddr string `toml:"https_server_listen_addr"`
	HTTPSCertFile   string `toml:"https_ssl_cert_file"`
	HTTPSKeyFile    string `toml:"https_ssl_key_file"`

	WebInterfaceEndpoint string `toml:"webinterface_endpoint"`

	HTTPGithubWebhookEndpoint string `toml:"github_webhook_endpoint"`
	GithubWebHookSecret       string `toml:"github_webhook_secret"`
	GithubAPIToken            string `toml:"github_api_token"`

	PrometheusMetricsEndpoint string `toml:"prometheus_metrics_endpoint"`

	LogFormat  string `toml:"log_format"`
	LogTimeKey string `toml:"log_time_key"`
	LogLevel   string `toml:"log_level"`

	TriggerOnAutoMerge bool               `toml:"trigger_on_auto_merge"`
	TriggerOnLabels    []string           `toml:"trigger_labels"`
	HeadLabel          string             `toml:"queue_pr_head_label"`
	Repositories       []GithubRepository `toml:"repository"`
}

type GithubRepository struct {
	Owner          string `toml:"owner"`
	RepositoryName string `toml:"repository"`
}

func Load(reader io.Reader) (*Config, error) {
	var result Config

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	if err := toml.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (r *Config) Marshal(writer io.Writer) error {
	return toml.NewEncoder(writer).Encode(r)
}
