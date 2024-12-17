package autoupdater

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

const metricNamespace = "directorius"

const (
	queueOperationsMetricName = "queue_operations_total"
	githubEventsMetricName    = "processed_github_events_total"
	queuedPRCountMetricName   = "queued_prs_count"
	timeToMergeMetricName     = "time_to_merge_seconds"
)

const (
	baseBranchLabel = "base_branch"
	repositoryLabel = "repository"
	operationLabel  = "operation"
	stateLabel      = "state"
)

type operationLabelVal string

const (
	operationLabelEnqueueVal operationLabelVal = "enqueue"
	operationLabelDequeueVal operationLabelVal = "dequeue"
)

type stateLabelVal string

const (
	stateLabelActiveVal    stateLabelVal = "active"
	stateLabelSuspendedVal stateLabelVal = "suspended"
)

type metricCollector struct {
	logger          *zap.Logger
	queueOps        *prometheus.CounterVec
	processedEvents prometheus.Counter
	queueSize       *prometheus.GaugeVec
	timeToMerge     *prometheus.SummaryVec
}

var metrics = newMetricCollector()

func newMetricCollector() *metricCollector {
	return &metricCollector{
		logger: zap.L().Named(loggerName).Named("metrics"),
		queueOps: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: metricNamespace,
				Name:      queueOperationsMetricName,
				Help:      "count of queue operations",
			},
			[]string{repositoryLabel, baseBranchLabel, operationLabel},
		),
		processedEvents: promauto.NewCounter(
			prometheus.CounterOpts{
				Namespace: metricNamespace,
				Name:      githubEventsMetricName,
				Help:      "count of processed github webhook events",
			},
		),
		queueSize: promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricNamespace,
				Name:      queuedPRCountMetricName,
				Help:      "count of processed github webhook events",
			},
			[]string{repositoryLabel, baseBranchLabel, stateLabel},
		),
		timeToMerge: promauto.NewSummaryVec(
			prometheus.SummaryOpts{
				Namespace: metricNamespace,
				Name:      timeToMergeMetricName,
				MaxAge:    24 * time.Hour,
				Objectives: map[float64]float64{
					0.5:  0.05,
					0.9:  0.01,
					0.99: 0.001,
				},
				Help: "time from adding a pull request to the merge queue until it gets merged",
			},
			[]string{repositoryLabel},
		),
	}
}

func (m *metricCollector) logGetMetricFailed(metricName string, err error) {
	m.logger.Warn("could not record metric",
		zap.String("metric", metricName),
		zap.Error(err),
	)
}

func enqueueOpsLabels(branchID *BranchID, operation operationLabelVal) prometheus.Labels {
	return prometheus.Labels{
		repositoryLabel: repositoryLabelValue(branchID.RepositoryOwner, branchID.Repository),
		baseBranchLabel: branchID.Branch,
		operationLabel:  string(operation),
	}
}

func (m *metricCollector) EnqueueOpsInc(baseBranchID *BranchID) {
	cnt, err := m.queueOps.GetMetricWith(enqueueOpsLabels(baseBranchID, operationLabelEnqueueVal))
	if err != nil {
		m.logGetMetricFailed("enqueue_ops_total", err)
		return
	}

	cnt.Inc()
}

func (m *metricCollector) DequeueOpsInc(baseBranchID *BranchID) {
	cnt, err := m.queueOps.GetMetricWith(enqueueOpsLabels(baseBranchID, operationLabelDequeueVal))
	if err != nil {
		m.logGetMetricFailed(queueOperationsMetricName, err)
		return
	}

	cnt.Inc()
}

func (m *metricCollector) ProcessedEventsInc() {
	m.processedEvents.Inc()
}

func (m *metricCollector) RecordTimeToMerge(d time.Duration, repositoryOwner, repository string) {
	s, err := m.timeToMerge.GetMetricWith(
		prometheus.Labels{repositoryLabel: repositoryLabelValue(repositoryOwner, repository)})
	if err != nil {
		m.logGetMetricFailed(timeToMergeMetricName, err)
		return
	}

	s.Observe(d.Seconds())
}
