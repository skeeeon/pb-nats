package connection

import (
	"errors"
	"testing"
	"time"

	pbtypes "github.com/skeeeon/pb-nats/internal/types"
	"github.com/skeeeon/pb-nats/internal/utils"
)

func newTestManager() *Manager {
	return NewManager(
		"nats://localhost:4222",
		nil,
		&pbtypes.RetryConfig{
			MaxPrimaryRetries: 4,
			InitialBackoff:    1 * time.Second,
			MaxBackoff:        8 * time.Second,
			BackoffMultiplier: 2.0,
			FailbackInterval:  30 * time.Second,
		},
		nil,
		utils.NewLogger(false),
	)
}

func TestCalculateBackoff(t *testing.T) {
	cm := newTestManager()

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 8 * time.Second}, // capped at MaxBackoff
		{10, 8 * time.Second},
	}
	for _, tt := range tests {
		if got := cm.calculateBackoff(tt.attempt); got != tt.want {
			t.Errorf("calculateBackoff(%d) = %v, want %v", tt.attempt, got, tt.want)
		}
	}
}

func TestIsRetryableError(t *testing.T) {
	cm := newTestManager()

	retryable := []error{
		errors.New("dial tcp: connection refused"),
		errors.New("read tcp: connection reset by peer"),
		errors.New("request timeout"),
		errors.New("no servers available for connection"),
	}
	for _, err := range retryable {
		if !cm.isRetryableError(err) {
			t.Errorf("isRetryableError(%v) = false, want true", err)
		}
	}

	notRetryable := []error{
		nil,
		errors.New("authorization violation"),
		errors.New("invalid JWT"),
	}
	for _, err := range notRetryable {
		if cm.isRetryableError(err) {
			t.Errorf("isRetryableError(%v) = true, want false", err)
		}
	}
}

func TestFailoverStateString(t *testing.T) {
	tests := map[FailoverState]string{
		StateHealthy:       "healthy",
		StateRetrying:      "retrying",
		StateFailedOver:    "failed_over",
		StateBootstrapping: "bootstrapping",
		FailoverState(99):  "unknown",
	}
	for state, want := range tests {
		if got := state.String(); got != want {
			t.Errorf("FailoverState(%d).String() = %q, want %q", state, got, want)
		}
	}
}
