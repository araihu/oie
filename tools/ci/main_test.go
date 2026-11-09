package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestConnectionWithoutOverrideUsesPinnedEngine(t *testing.T) {
	params := connectionParams("")
	if !strings.HasPrefix(params.RunnerHost, "image://registry.dagger.io/engine:v0.21.9@sha256:") || params.Version != "v0.21.9" {
		t.Fatalf("default connection is not pinned: %+v", params)
	}
}

func TestConnectionPreservesConfiguredRunner(t *testing.T) {
	for _, host := range []string{"unix:///run/dagger/engine.sock", "docker-container://existing-engine", "tcp://127.0.0.1:1234"} {
		if got := connectionParams(host).RunnerHost; got != host {
			t.Fatalf("configured runner %q became %q", host, got)
		}
	}
}

func TestBrowserStatusCannotBeMaskedBySuccessfulArtifactExport(t *testing.T) {
	for _, tc := range []struct {
		value   string
		success bool
	}{
		{"0\n", true}, {"0", true}, {"1\n", false}, {"23\n", false}, {"137\n", false}, {"", false}, {"0\n1", false}, {"missing", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if err := browserOutcome(tc.value); (err == nil) != tc.success {
				t.Fatalf("exit receipt %q returned %v", tc.value, err)
			}
		})
	}
}

func TestExecutePreservesTaskResult(t *testing.T) {
	failure := errors.New("fixture failure")
	for _, want := range []error{nil, failure} {
		if got := execute(context.Background(), time.Second, func() error { return want }); !errors.Is(got, want) {
			t.Fatalf("execute returned %v, want %v", got, want)
		}
	}
}

func TestExecuteBoundsBlockedCleanupAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	blocked := make(chan struct{})
	defer close(blocked)
	cancel()
	start := time.Now()
	err := execute(ctx, 10*time.Millisecond, func() error { <-blocked; return nil })
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "cleanup exceeded") {
		t.Fatalf("blocked cleanup returned %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("blocked cleanup ignored cancellation grace")
	}
}

func TestExecuteCancellationCannotBecomeSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := execute(ctx, time.Second, func() error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled task returned %v", err)
	}
}

func TestExecuteDeadlineIncludesBlockedSessionClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	blocked := make(chan struct{})
	defer close(blocked)
	err := execute(ctx, 10*time.Millisecond, func() error { <-blocked; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "cleanup exceeded") {
		t.Fatalf("deadline with blocked session close returned %v", err)
	}
}
