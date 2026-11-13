package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"

	"dagger.io/dagger"
)

type releaseIdentity struct {
	Version             string `json:"version"`
	Revision            string `json:"revision"`
	PackagingRevision   string `json:"packagingRevision"`
	SourceArchiveSHA256 string `json:"sourceArchiveSHA256"`
}

func decodeReleaseIdentity(data string) (releaseIdentity, error) {
	var identity releaseIdentity
	if err := json.Unmarshal([]byte(data), &identity); err != nil {
		return identity, err
	}
	version := regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
	sha := regexp.MustCompile(`^[0-9a-f]{40}$`)
	checksum := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if !version.MatchString(identity.Version) || !sha.MatchString(identity.Revision) ||
		!sha.MatchString(identity.PackagingRevision) || !checksum.MatchString(identity.SourceArchiveSHA256) {
		return identity, errors.New("release requires a version, full source/packaging revisions and source archive SHA256")
	}
	return identity, nil
}

// release builds the immutable tagged app with the current packaging recipe.
// Its runtime check installs the actual archive into a fresh Ubuntu container
// containing runtime libraries and GUI test tools, without Go or build headers.
func (r runner) release(ctx context.Context, source *dagger.Directory) error {
	data, err := source.File(".release-identity.json").Contents(ctx)
	if err != nil {
		return err
	}
	identity, err := decodeReleaseIdentity(data)
	if err != nil {
		return err
	}
	platform := dagger.Platform("linux/amd64")
	toolchain := r.client.Container(dagger.ContainerOpts{Platform: platform}).From(goImage)
	base := r.client.Container(dagger.ContainerOpts{Platform: platform}).From(desktopImage).
		WithEnvVariable("DEBIAN_FRONTEND", "noninteractive").
		WithExec([]string{"sh", "-c", "apt-get update -qq && apt-get install -y --no-install-recommends build-essential pkg-config libgtk-4-dev libwebkitgtk-6.0-dev xvfb xauth xdotool scrot x11-utils dbus-x11 python3 ca-certificates curl && rm -rf /var/lib/apt/lists/*"}).
		WithDirectory("/usr/local/go", toolchain.Directory("/usr/local/go")).
		WithEnvVariable("PATH", "/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin").
		WithEnvVariable("GOPATH", "/go").
		WithMountedCache("/go/pkg/mod", r.client.CacheVolume("oie-ci-go-mod-v1")).
		WithMountedCache("/root/.cache/go-build", r.client.CacheVolume("oie-ci-desktop-go-build-amd64-v1")).
		WithEnvVariable("GOMAXPROCS", "4").WithEnvVariable("GOWORK", "off").
		WithEnvVariable("GOFLAGS", "").WithEnvVariable("CGO_ENABLED", "1")
	base, err = r.stage(ctx, "release-build-setup-amd64", base)
	if err != nil {
		return err
	}
	built := base.WithDirectory("/app", source.Directory(".release-source")).
		WithDirectory("/tooling/scripts", source.Directory("scripts")).
		WithDirectory("/tooling/packaging", source.Directory("packaging")).
		WithDirectory("/tooling/tests/packaging", source.Directory("tests/packaging")).
		WithFile("/tooling/docs/release-source.md", source.File("docs/release-source.md")).
		WithFile("/out/source-receipt.json", source.File(".release-identity.json")).
		WithWorkdir("/app").
		WithExec([]string{"sh", "/tooling/scripts/package-desktop.sh", "--version", identity.Version, "--revision", identity.Revision, "--source", "/app", "--output", "/out"})
	built, err = r.stage(ctx, "release-package-amd64", built)
	if err != nil {
		return err
	}
	archive := "oie_" + identity.Version + "_linux_amd64.tar.gz"
	runtime := r.client.Container(dagger.ContainerOpts{Platform: platform}).From(desktopImage).
		WithEnvVariable("DEBIAN_FRONTEND", "noninteractive").
		WithExec([]string{"sh", "-c", "apt-get update -qq && apt-get install -y --no-install-recommends ca-certificates libgtk-4-1 libwebkitgtk-6.0-4 libsoup-3.0-0 xvfb xauth xdotool scrot dbus-x11 python3 && rm -rf /var/lib/apt/lists/*"})
	runtime, err = r.stage(ctx, "release-runtime-setup-amd64", runtime)
	if err != nil {
		return err
	}
	runtime = runtime.WithFile("/release/"+archive, built.File("/out/"+archive)).
		WithFile("/tooling/install.sh", source.File("scripts/install.sh")).
		WithDirectory("/tooling/tests/packaging", source.Directory("tests/packaging")).
		WithEnvVariable("LIBGL_ALWAYS_SOFTWARE", "1").WithEnvVariable("GDK_BACKEND", "x11").
		// Isolated GUI test container only; never set in installed application resources.
		WithEnvVariable("WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS", "1").
		WithExec([]string{"dbus-run-session", "--", "xvfb-run", "-a", "-s", "-screen 0 1440x1000x24", "python3", "/tooling/tests/packaging/smoke_release.py", "--archive", "/release/" + archive, "--installer", "/tooling/install.sh", "--version", identity.Version, "--revision", identity.Revision, "--artifacts", "/evidence"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	runtime, err = r.stage(ctx, "release-installed-runtime-amd64", runtime)
	if err != nil {
		return err
	}
	_, evidenceErr := runtime.Directory("/evidence").Export(ctx, filepath.Join(r.logs, "runtime"))
	code, codeErr := runtime.ExitCode(ctx)
	if code != 0 {
		return errors.Join(evidenceErr, codeErr, fmt.Errorf("packaged desktop runtime exited %d", code))
	}
	if evidenceErr != nil {
		return evidenceErr
	}
	_, err = built.Directory("/out").Export(ctx, filepath.Join(r.logs, "release"))
	return err
}
