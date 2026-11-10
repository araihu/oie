package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"dagger.io/dagger"
)

// The release Linux target is amd64 even when the development/engine host is
// arm64. Pin the image platform so a successful native lane verifies that target.
func (r runner) desktop(ctx context.Context, source *dagger.Directory) error {
	platform := dagger.Platform("linux/amd64")
	toolchain := r.client.Container(dagger.ContainerOpts{Platform: platform}).From(goImage)
	base := r.client.Container(dagger.ContainerOpts{Platform: platform}).From(desktopImage).
		WithEnvVariable("DEBIAN_FRONTEND", "noninteractive").
		WithExec([]string{"sh", "-c", "apt-get update -qq && apt-get install -y --no-install-recommends build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev xvfb xauth xdotool dbus-x11 python3 ca-certificates curl && rm -rf /var/lib/apt/lists/*"}).
		WithDirectory("/usr/local/go", toolchain.Directory("/usr/local/go")).
		WithEnvVariable("PATH", "/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin").
		WithEnvVariable("GOPATH", "/go").
		WithMountedCache("/go/pkg/mod", r.client.CacheVolume("oie-ci-go-mod-v1")).
		WithMountedCache("/root/.cache/go-build", r.client.CacheVolume("oie-ci-desktop-go-build-amd64-v1")).
		WithMountedCache("/root/.cache/golangci-lint", r.client.CacheVolume("oie-ci-desktop-lint-amd64-v1")).
		WithEnvVariable("GOMAXPROCS", "4").WithEnvVariable("GOWORK", "off").
		WithEnvVariable("GOFLAGS", "").WithEnvVariable("CGO_ENABLED", "1")
	base, err := r.stage(ctx, "desktop-setup-amd64", base)
	if err != nil {
		return err
	}
	built := base.WithDirectory("/src", source).WithWorkdir("/src").
		WithExec([]string{"make", "build-desktop"}).
		WithExec([]string{"go", "tool", "-modfile=tools/go.mod", "golangci-lint", "run", "--build-tags=production", "./internal/adapters/desktop/...", "./cmd/oie-desktop/..."}).
		WithExec([]string{"go", "vet", "-tags=production", "./internal/adapters/desktop", "./cmd/oie-desktop"}).
		WithExec([]string{"go", "test", "-race", "./internal/adapters/desktop", "./internal/apphost"})
	built, err = r.stage(ctx, "desktop-build-amd64", built)
	if err != nil {
		return err
	}
	checked := built.WithEnvVariable("LIBGL_ALWAYS_SOFTWARE", "1").
		WithEnvVariable("GDK_BACKEND", "x11").
		// Test containers cannot provide WebKit's normal user-namespace sandbox.
		// This override never enters the installed application's environment.
		WithEnvVariable("WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS", "1").
		WithExec([]string{"sh", "tools/ci/desktop.sh"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	checked, err = r.stage(ctx, "desktop-webview-amd64", checked)
	if err != nil {
		return err
	}
	_, exportErr := checked.Directory("/src/tests/desktop/artifacts").Export(ctx, filepath.Join(r.logs, "desktop"))
	code, codeErr := checked.ExitCode(ctx)
	if code != 0 {
		return errors.Join(exportErr, codeErr, fmt.Errorf("desktop WebView failed with exit %d", code))
	}
	return errors.Join(exportErr, codeErr)
}
