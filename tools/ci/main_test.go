package main

import (
	"strings"
	"testing"
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
