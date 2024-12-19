package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"time"

	"github.com/simplesurance/directorius/internal/autoupdater"
	"github.com/simplesurance/directorius/internal/cfg"
	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/jenkins"
	"github.com/simplesurance/directorius/internal/provider/github"
	"github.com/simplesurance/directorius/internal/retry"
	"github.com/simplesurance/directorius/internal/set"

	"github.com/spf13/pflag"
	zaplogfmt "github.com/sykesm/zap-logfmt"
	"github.com/thecodeteam/goodbye"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	appName       = "directorius"
	defConfigFile = "/etc/" + appName + "/config.toml"
)

var logger *zap.Logger

// Version is set by goreleaser
var Version = "version unknown"

const EventChannelBufferSize = 1024

func exitOnErr(msg string, err error) {
	if err == nil {
		return
	}

	fmt.Fprintln(os.Stderr, "ERROR:", msg+", error:", err.Error())
	os.Exit(1)
}

func panicHandler() {
	if r := recover(); r != nil {
		logger.Error(
			"panic caught, terminating gracefully",
			zap.String("panic", fmt.Sprintf("%v", r)),
			zap.StackSkip("stacktrace", 1),
		)

		ctx, cancelFn := context.WithTimeout(context.Background(), time.Minute)
		defer cancelFn()

		goodbye.Exit(ctx, 1)
	}
}

func startHTTPSServer(listenAddr, certFile, keyFile string, mux *http.ServeMux) {
	httpsServer := http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	goodbye.Register(func(context.Context, os.Signal) {
		const shutdownTimeout = 30 * time.Second
		ctx, cancelFn := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancelFn()

		logger.Debug(
			"terminating https server",
			zap.Duration("shutdown_timeout", shutdownTimeout),
		)

		err := httpsServer.Shutdown(ctx)
		if err != nil {
			logger.Warn(
				"shutting down https server failed",
				zap.Error(err),
			)
		}
	})

	go func() {
		defer panicHandler()

		logger.Info(
			"https server started",
			zap.String("listenAddr", listenAddr),
		)

		err := httpsServer.ListenAndServeTLS(certFile, keyFile)
		if errors.Is(err, http.ErrServerClosed) {
			logger.Info("https server terminated")
			return
		}

		logger.Fatal(
			"https server terminated unexpectedly",
			zap.Error(err),
		)
	}()
}

func startHTTPServer(listenAddr string, mux *http.ServeMux) {
	httpServer := http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	goodbye.Register(func(context.Context, os.Signal) {
		const shutdownTimeout = 30 * time.Second
		ctx, cancelFn := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancelFn()

		logger.Debug(
			"terminating http server",
			zap.Duration("shutdown_timeout", shutdownTimeout),
		)

		err := httpServer.Shutdown(ctx)
		if err != nil {
			logger.Warn(
				"shutting down http server failed",
				zap.Error(err),
			)
		}
	})

	go func() {
		defer panicHandler()

		logger.Info(
			"http server started",
			zap.String("listenAddr", listenAddr),
		)

		err := httpServer.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			logger.Info("http server terminated")
			return
		}

		logger.Fatal(
			"http server terminated unexpectedly",
			zap.Error(err),
		)
	}()
}

type arguments struct {
	Verbose     *bool
	ConfigFile  *string
	ShowVersion *bool
	DryRun      *bool
}

var args arguments

func mustParseCommandlineParams() {
	args = arguments{
		Verbose: pflag.BoolP(
			"verbose",
			"v",
			false,
			"enable verbose logging",
		),
		ConfigFile: pflag.StringP(
			"cfg-file",
			"c",
			defConfigFile,
			"path to the configuration file",
		),
		ShowVersion: pflag.Bool(
			"version",
			false,
			"print the version and exit",
		),
		DryRun: pflag.Bool(
			"dry-run",
			false,
			"simulate operations that would result in changes",
		),
	}

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTION]\nReceive GitHub webHook events and trigger actions.\n", appName)
		fmt.Fprintf(os.Stderr, "\nOptions:\n")
		pflag.PrintDefaults()
	}

	pflag.Parse()
}

func mustParseCfg() *cfg.Config {
	// we use exitOnErr in this function instead of logger.Fatal() because
	// the logger is not initialized yet

	file, err := os.Open(*args.ConfigFile)
	exitOnErr("could not open configuration files", err)
	defer file.Close()

	config, err := cfg.Load(file)
	if err != nil {
		exitOnErr(fmt.Sprintf("could not load configuration file: %s", *args.ConfigFile), err)
	}

	config.WebInterfaceEndpoint = normalizeHTTPEndpoint(config.WebInterfaceEndpoint)

	return config
}

func initLogFmtLogger(config *cfg.Config, logLevel zapcore.Level) *zap.Logger {
	cfg := zapEncoderConfig(config)

	logger := zap.New(zapcore.NewCore(
		zaplogfmt.NewEncoder(cfg),
		os.Stdout,
		logLevel),
	)

	return logger
}

func zapEncoderConfig(config *cfg.Config) zapcore.EncoderConfig {
	cfg := zap.NewProductionEncoderConfig()

	cfg.LevelKey = "loglevel"
	cfg.TimeKey = config.LogTimeKey
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncodeDuration = zapcore.StringDurationEncoder

	return cfg
}

func mustInitZapFormatLogger(config *cfg.Config, logLevel zapcore.Level) *zap.Logger {
	cfg := zap.NewProductionConfig()
	cfg.Sampling = nil
	cfg.EncoderConfig = zapEncoderConfig(config)
	cfg.OutputPaths = []string{"stdout"}
	cfg.Encoding = config.LogFormat
	cfg.Level = zap.NewAtomicLevelAt(logLevel)

	logger, err := cfg.Build()
	exitOnErr("could not initialize logger", err)

	return logger
}

func mustInitLogger(config *cfg.Config) {
	var logLevel zapcore.Level
	if *args.Verbose {
		logLevel = zapcore.DebugLevel
	} else {
		if err := (&logLevel).Set(config.LogLevel); err != nil {
			fmt.Fprintf(os.Stderr, "can not set log level to %q: %s \n", config.LogFormat, err)
			os.Exit(2)
		}
	}

	switch config.LogFormat {
	case "logfmt":
		logger = initLogFmtLogger(config, logLevel)
	case "console", "json":
		logger = mustInitZapFormatLogger(config, logLevel)
	default:
		fmt.Fprintf(os.Stderr, "unsupported log-format argument: %q\n", config.LogFormat)
		os.Exit(2)
	}

	logger = logger.Named("main")
	zap.ReplaceGlobals(logger)
	// TODO: call goodbye.Exit on Fatal and Panic calls (zap.WithFatalHook)

	goodbye.Register(func(context.Context, os.Signal) {
		if err := logger.Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "flushing logs failed: %s\n", err)
		}
	})
}

func hide(in string) string {
	if in == "" {
		return in
	}

	return "**hidden**"
}

func normalizeHTTPEndpoint(endpoint string) string {
	if len(endpoint) != 0 && endpoint[len(endpoint)-1] != '/' {
		return endpoint + "/"
	}

	return endpoint
}

func mustConfigCItoAutoupdaterCI(cfg *cfg.CI) *autoupdater.CI {
	if cfg == nil {
		return nil
	}

	if len(cfg.Jobs) > 0 && cfg.ServerURL == "" {
		fmt.Fprintf(os.Stderr, "ERROR: config file: %s: ci.jobs are defined but ci.server_url is empty\n", *args.ConfigFile)
		os.Exit(1)
	}

	if cfg.ServerURL != "" && len(cfg.Jobs) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: config file: %s: ci.server_url is defined but no ci.jobs are defined\n", *args.ConfigFile)
		os.Exit(1)

	}

	var result autoupdater.CI
	var err error
	result.Client, err = jenkins.NewClient(zap.L(), cfg.ServerURL, cfg.BasicAuth.User, cfg.BasicAuth.Password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: config file: %s: ci.server_url is defined but no ci.jobs are defined\n", *args.ConfigFile)
		os.Exit(1)
	}
	for _, job := range cfg.Jobs {
		result.Jobs = append(result.Jobs, &jenkins.JobTemplate{
			RelURL:     job.URLPath,
			Parameters: job.Parameters,
		})
	}
	return &result
}

func mustStartPullRequestAutoupdater(config *cfg.Config, ch chan *github.Event, githubClient *githubclt.Client, mux *http.ServeMux) *autoupdater.Autoupdater {
	repos := make([]autoupdater.Repository, 0, len(config.Repositories))
	for _, r := range config.Repositories {
		repos = append(repos, autoupdater.Repository{
			OwnerLogin:     r.Owner,
			RepositoryName: r.RepositoryName,
		})
	}

	au := autoupdater.NewAutoupdater(
		autoupdater.Config{
			GitHubClient:          githubClient,
			EventChan:             ch,
			Retryer:               retry.NewRetryer(),
			MonitoredRepositories: set.From(repos),
			TriggerOnAutomerge:    config.TriggerOnAutoMerge,
			TriggerLabels:         set.From(config.TriggerOnLabels),
			HeadLabel:             config.HeadLabel,
			DryRun:                *args.DryRun,
			CI:                    mustConfigCItoAutoupdaterCI(&config.CI),
		},
	)

	ctx, cancelFn := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancelFn()
	if err := au.InitSync(ctx); err != nil {
		logger.Error(
			"autoupdater: initial synchronization failed",
			zap.Error(err),
		)
	}

	au.Start()

	if config.WebInterfaceEndpoint != "" {
		autoupdater.NewHTTPService(au, config.WebInterfaceEndpoint).RegisterHandlers(mux)
		logger.Info(
			"registered github pull request autoupdater http endpoint",
			zap.String("endpoint", config.WebInterfaceEndpoint),
		)
	}

	return au
}

func mustValidateConfig(config *cfg.Config) {
	if config.HTTPListenAddr == "" && config.HTTPSListenAddr == "" {
		fmt.Fprintf(os.Stderr, "https_server_listen_addr or http_server_listen_addr must be defined in the config file, both are unset")
		os.Exit(1)
	}

	if !config.TriggerOnAutoMerge && len(config.TriggerOnLabels) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: config file: %s: trigger_on_auto_merge must be true or trigger_labels must be defined, both are empty in the configuration file\n", *args.ConfigFile)
		os.Exit(1)
	}

	if len(config.HeadLabel) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: config file %s: queue_pr_head_label must be provided when autoupdater is enabled\n", *args.ConfigFile)
		os.Exit(1)
	}

	if len(config.GithubAPIToken) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: config file %s: github_api_token must be provided when autoupdater is enabled\n", *args.ConfigFile)
		os.Exit(1)
	}

	if len(config.HTTPGithubWebhookEndpoint) == 0 {
		fmt.Fprintf(os.Stderr, "ERROR: config file %s: github_webhook_endpoint must be provided when autoupdater is enabled\n", *args.ConfigFile)
		os.Exit(1)
	}

	if len(config.Repositories) == 0 {
		logger.Info("github pull request updater is disabled, autoupdater.repository config field is empty")
	}
}

func main() {
	defer panicHandler()

	goodbye.Notify(context.Background())

	mustParseCommandlineParams()

	if *args.ShowVersion {
		fmt.Printf("%s %s\n", appName, Version)
		os.Exit(0) // nolint:gocritic // defer functions won't run
	}

	config := mustParseCfg()
	mustValidateConfig(config)

	mustInitLogger(config)

	logger.Info("loaded cfg file",
		zap.String("cfg_file", *args.ConfigFile),
		zap.String("http_server_listen_addr", config.HTTPListenAddr),
		zap.String("https_server_listen_addr", config.HTTPSListenAddr),
		zap.String("github_webhook_endpoint", config.HTTPGithubWebhookEndpoint),
		zap.String("github_webhook_secret", hide(config.GithubWebHookSecret)),
		zap.String("github_api_token", hide(config.GithubAPIToken)),
		zap.String("log_format", config.LogFormat),
		zap.String("log_time_key", config.LogTimeKey),
		zap.String("log_level", config.LogLevel),
		zap.String("prometheus_metrics_endpoint", config.PrometheusMetricsEndpoint),
		zap.Bool("trigger_on_auto_merge", config.TriggerOnAutoMerge),
		zap.Strings("trigger_labels", config.TriggerOnLabels),
		zap.Any("repositories", config.Repositories),
		zap.String("webinterface_endpoint", config.WebInterfaceEndpoint),
		zap.String("ci.base_url", config.CI.ServerURL),
		zap.String("ci.basic_auth.user", config.CI.BasicAuth.User),
		zap.String("ci.basic_auth.password", hide(config.CI.BasicAuth.User)),
		zap.Any("ci.job", config.CI.Jobs),
	)

	githubClient := githubclt.New(config.GithubAPIToken)

	goodbye.RegisterWithPriority(func(_ context.Context, sig os.Signal) {
		logger.Info(fmt.Sprintf("terminating, received signal %s", sig.String()))
	}, math.MinInt)

	mux := http.NewServeMux()

	ch := make(chan *github.Event, EventChannelBufferSize)
	gh := github.New(
		[]chan<- *github.Event{ch},
		github.WithPayloadSecret(config.GithubWebHookSecret),
	)
	mux.HandleFunc(config.HTTPGithubWebhookEndpoint, gh.HTTPHandler)
	logger.Info(
		"registered github webhook event http endpoint",
		zap.String("endpoint", config.HTTPGithubWebhookEndpoint),
	)

	if config.PrometheusMetricsEndpoint != "" {
		mux.Handle(config.PrometheusMetricsEndpoint, promhttp.Handler())
		logger.Info(
			"registered prometheus metrics http endpoint",
			zap.String("endpoint", config.PrometheusMetricsEndpoint),
		)
	}

	if config.HTTPListenAddr != "" {
		startHTTPServer(config.HTTPListenAddr, mux)
	}

	if config.HTTPSListenAddr != "" {
		startHTTPSServer(
			config.HTTPSListenAddr,
			config.HTTPSCertFile,
			config.HTTPSKeyFile,
			mux,
		)
	}

	autoupdater := mustStartPullRequestAutoupdater(config, ch, githubClient, mux)

	waitForTermCh := make(chan struct{})
	goodbye.RegisterWithPriority(func(context.Context, os.Signal) {
		autoupdater.Stop()
		close(waitForTermCh)
	}, -1)

	<-waitForTermCh
}
