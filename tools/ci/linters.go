package main

import (
	"context"
	"fmt"
	"strings"

	"dagger.io/dagger"
)

// Keep the release binaries in sync with tools/go.mod and tools/vacuum/go.mod.
const (
	vacuumVersion       = "0.39.3"
	golangciLintVersion = "2.15.0"
)

func (r runner) withLinters(ctx context.Context, base *dagger.Container) (*dagger.Container, error) {
	platform, err := r.client.DefaultPlatform(ctx)
	if err != nil {
		return nil, err
	}
	_, arch, _ := strings.Cut(string(platform), "/")
	arch, _, _ = strings.Cut(arch, "/")
	var vacuumArch, vacuumSHA string
	switch arch {
	case "amd64":
		vacuumArch, vacuumSHA = "x86_64", "fd08ed9bb27f84c740008029ba992681046479ea5f745108ea3fd87d3988fd47"
	case "arm64":
		vacuumArch, vacuumSHA = "arm64", "57052b0ca332508d04f7e7cbae5c8e954e8ae5fa1d529837386c7b42da7d7900"
	default:
		return nil, fmt.Errorf("no precompiled CI linters for %s", platform)
	}
	vacuumURL := fmt.Sprintf("https://github.com/daveshanley/vacuum/releases/download/v%s/vacuum_%s_linux_%s.tar.gz", vacuumVersion, vacuumVersion, vacuumArch)
	vacuum := r.releaseBinary(vacuumURL, vacuumSHA, "vacuum")
	options := dagger.ContainerWithFileOpts{Permissions: 0o755}
	return base.
		WithFile("/usr/local/bin/vacuum", vacuum, options).
		WithFile("/usr/local/bin/golangci-lint", r.golangciLint(arch), options).
		WithEnvVariable("VACUUM", "/usr/local/bin/vacuum").
		WithEnvVariable("GOLANGCI_LINT", "/usr/local/bin/golangci-lint"), nil
}

func (r runner) golangciLint(arch string) *dagger.File {
	digests := map[string]string{
		"amd64": "9b7fe6a6bf6334b98decf8d86b11415ea7889afdabdd1959fdce23af2a9ebcea",
		"arm64": "b4d8f17068b35ee6060cc2f69a16fb48740d706fbba81661c318649c114a6998",
	}
	release := fmt.Sprintf("golangci-lint-%s-linux-%s", golangciLintVersion, arch)
	url := fmt.Sprintf("https://github.com/golangci/golangci-lint/releases/download/v%s/%s.tar.gz", golangciLintVersion, release)
	return r.releaseBinary(url, digests[arch], release+"/golangci-lint")
}

func (r runner) releaseBinary(url, sha256, member string) *dagger.File {
	archive := r.client.HTTP(url, dagger.HTTPOpts{Checksum: "sha256:" + sha256})
	return r.client.Container().From(goImage).
		WithMountedFile("/tmp/release.tar.gz", archive).
		WithExec([]string{"sh", "-ec", `tar -xzOf /tmp/release.tar.gz "$1" > /tmp/release-bin`, "sh", member}).
		File("/tmp/release-bin")
}
