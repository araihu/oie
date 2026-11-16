package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger"
)

//go:embed linters.json
var linterReleases []byte

type linterRelease struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Member string `json:"member"`
}

func (r runner) withLinters(ctx context.Context, base *dagger.Container) (*dagger.Container, error) {
	platform, err := r.client.DefaultPlatform(ctx)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"vacuum", "golangci-lint"} {
		base, err = r.withLinter(base, name, platform)
		if err != nil {
			return nil, err
		}
	}
	return base.
		WithEnvVariable("VACUUM", "/usr/local/bin/vacuum").
		WithEnvVariable("GOLANGCI_LINT", "/usr/local/bin/golangci-lint"), nil
}

func (r runner) withLinter(base *dagger.Container, name string, platform dagger.Platform) (*dagger.Container, error) {
	var releases map[string]map[string]linterRelease
	if err := json.Unmarshal(linterReleases, &releases); err != nil {
		return nil, fmt.Errorf("read pinned linters: %w", err)
	}
	system, arch, _ := strings.Cut(string(platform), "/")
	arch, _, _ = strings.Cut(arch, "/")
	release, ok := releases[name][system+"/"+arch]
	if !ok {
		return nil, fmt.Errorf("no precompiled %s for %s", name, platform)
	}
	archive := r.client.HTTP(release.URL, dagger.HTTPOpts{Checksum: "sha256:" + release.SHA256})
	binary := r.client.Container().From(goImage).
		WithMountedFile("/tmp/release.tar.gz", archive).
		WithExec([]string{"sh", "-ec", `tar -xzOf /tmp/release.tar.gz "$1" > /tmp/release-bin`, "sh", release.Member}).
		File("/tmp/release-bin")
	return base.WithFile("/usr/local/bin/"+name, binary, dagger.ContainerWithFileOpts{Permissions: 0o755}), nil
}
