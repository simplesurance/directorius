package mergequeue

import (
	"slices"

	"github.com/simplesurance/directorius/internal/githubclt"
	"github.com/simplesurance/directorius/internal/set"
)

const (
	reasonPullRequestClosed = "pull request is closed"
	reasonTriggerNotMet     = "pull request does not meet the queue trigger condition, " +
		"auto-merge is not enabled and it has no trigger label"
)

// enqueueTrigger defines the conditions of which at least one must be met for
// a pull request to be in a merge queue.
// It mirrors [Config.TriggerOnAutomerge] and [Config.TriggerLabels].
type enqueueTrigger struct {
	// OnAutomerge is true if pull requests with enabled auto-merge belong
	// in the queue.
	OnAutomerge bool
	// Labels are labels that cause pull requests to belong in the queue.
	Labels set.Set[string]
}

// isConfigured returns true if at least one trigger condition is defined.
// The configuration of the application requires it. A queue without a
// configured trigger does not verify the condition.
func (t enqueueTrigger) isConfigured() bool {
	return t.OnAutomerge || len(t.Labels) > 0
}

// isMet returns true if a pull request with the given auto-merge state and
// labels fulfills one of the trigger conditions.
// If no trigger is configured it always returns true.
func (t enqueueTrigger) isMet(autoMergeEnabled bool, labels []string) bool {
	if !t.isConfigured() {
		return true
	}

	if t.OnAutomerge && autoMergeEnabled {
		return true
	}

	return slices.ContainsFunc(labels, t.Labels.Contains)
}

// dequeueRequest describes why a pull request must be removed from the queue.
type dequeueRequest struct {
	Reason string
	// ResetStatus is true if the github commit status of the pull request
	// must be reset to pending when it is dequeued.
	// It is only needed for open pull requests.
	ResetStatus bool
}

// dequeueRequired checks if the pull request, that status describes, is still
// eligible to be in the queue.
// It is not eligible when it is closed or none of the trigger conditions is
// met anymore. GitHub webhook events that communicate this can be lost.
// If it is not eligible, a dequeueRequest is returned, otherwise nil.
func (q *queue) dequeueRequired(status *githubclt.ReadyForMergeStatus) *dequeueRequest {
	if status.Closed {
		return &dequeueRequest{Reason: reasonPullRequestClosed}
	}

	if !q.trigger.isMet(status.AutoMergeEnabled, status.Labels) {
		return &dequeueRequest{Reason: reasonTriggerNotMet, ResetStatus: true}
	}

	return nil
}
