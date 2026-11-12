// Command ci runs the Dagger graph without the interactive CLI frontend.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"dagger.io/dagger"
	engineclient "github.com/dagger/dagger/engine/client"
)

const (
	daggerVersion = "v0.21.9"
	daggerImage   = "registry.dagger.io/engine:v0.21.9@sha256:3c4e0cedaea400d7e30155e050f94910ea5f381b6f5942dd2172382657628cb4"
	goImage       = "golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea"
	dockerImage   = "docker:28-cli@sha256:625d9431a9f54c5a2bc90f24f0e1c3d55b1349fd857dd85035f98c2c9acbdd4d"
	xmppImage     = "prosodyim/prosody:13.0@sha256:c54842dec520463e3557a2d26df3312338003feb4cc943afa92d287a30c02b58"
	nodeImage     = "node:24.19.0-bookworm-slim@sha256:3638d9a6fe4030bd716be989438248074489337ba3275657f93595428be4fc03"
	desktopImage  = "ubuntu:24.04@sha256:fe81560990fae86dc4743202b2cb4d3df98d0bcf949173cecef15ce1127ef852"
	setupCommand  = "apt-get update -qq && apt-get install -y --no-install-recommends ffmpeg && rm -rf /var/lib/apt/lists/*"
)

type runner struct {
	client *dagger.Client
	logs   string
	output io.Writer
}

func main() {
	mode := flag.String("mode", "checks", "checks, desktop, release, or all")
	source := flag.String("source", "", "materialized source directory")
	artifacts := flag.String("artifacts", "", "host evidence directory")
	limit := flag.Duration("timeout", 30*time.Minute, "overall execution deadline")
	flag.Parse()
	if (*mode != "checks" && *mode != "desktop" && *mode != "release" && *mode != "all") || *source == "" || *artifacts == "" || *limit <= 0 {
		fmt.Fprintln(os.Stderr, "ci requires -mode checks|desktop|release|all, -source, -artifacts and a positive timeout")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *limit)
	defer cancel()
	if err := execute(ctx, 15*time.Second, func() error { return run(ctx, *mode, *source, *artifacts) }); err != nil {
		fmt.Fprintln(os.Stderr, "[ci] failed:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "[ci] finished successfully")
}

// execute gives session shutdown a bounded grace after cancellation. Returning
// lets main exit even when an upstream Close call does not honor cancellation.
func execute(ctx context.Context, grace time.Duration, task func() error) error {
	result := make(chan error, 1)
	go func() { result <- task() }()
	select {
	case err := <-result:
		return errors.Join(ctx.Err(), err)
	case <-ctx.Done():
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case err := <-result:
		return errors.Join(ctx.Err(), err)
	case <-timer.C:
		return fmt.Errorf("session cleanup exceeded cancellation grace: %w", ctx.Err())
	}
}

func run(ctx context.Context, mode, source, artifacts string) (err error) {
	if err = os.MkdirAll(artifacts, 0700); err != nil {
		return err
	}
	// The CLI subscribes to engine telemetry even in silent session mode. Its
	// forwarded HTTP/2 streams can stall completed queries and shutdown. Use
	// Dagger's public engine client directly, without telemetry subscriptions.
	engine, err := engineclient.Connect(ctx, connectionParams(os.Getenv("_EXPERIMENTAL_DAGGER_RUNNER_HOST")))
	if err != nil {
		return fmt.Errorf("connect to Dagger: %w", err)
	}
	defer func() { err = errors.Join(err, engine.Close()) }()
	client, err := dagger.Connect(ctx, dagger.WithConn(engineclient.EngineConn(engine)))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, client.Close()) }()
	r := runner{client: client, logs: artifacts, output: os.Stderr}
	return r.pipeline(ctx, mode, source)
}

func connectionParams(host string) engineclient.Params {
	if host == "" {
		host = "image://" + daggerImage
	}
	return engineclient.Params{RunnerHost: host, Version: daggerVersion}
}

func (r runner) stage(ctx context.Context, name string, container *dagger.Container) (*dagger.Container, error) {
	start := time.Now()
	fmt.Fprintf(r.output, "[ci] %s started\n", name)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				fmt.Fprintf(r.output, "[ci] %s running (%s)\n", name, time.Since(start).Round(time.Second))
			}
		}
	}()
	defer close(done)
	evaluated, err := container.Sync(ctx)
	if err != nil {
		var execution *dagger.ExecError
		if errors.As(err, &execution) {
			logErr := r.writeLog(name, execution.Stdout, execution.Stderr)
			err = errors.Join(err, logErr)
			fmt.Fprint(r.output, execution.Stdout, execution.Stderr)
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	stdout, err := evaluated.Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s stdout: %w", name, err)
	}
	stderr, err := evaluated.Stderr(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s stderr: %w", name, err)
	}
	if err := r.writeLog(name, stdout, stderr); err != nil {
		return nil, err
	}
	fmt.Fprintf(r.output, "[ci] %s completed (%s), log: %s\n", name, time.Since(start).Round(time.Second), filepath.Join(r.logs, name+".log"))
	return evaluated, nil
}
func (r runner) writeLog(name, stdout, stderr string) error {
	return os.WriteFile(filepath.Join(r.logs, name+".log"), []byte(stdout+"\n"+stderr), 0600)
}

func (r runner) pipeline(ctx context.Context, mode, sourcePath string) error {
	client := r.client
	source := client.Host().Directory(sourcePath)
	if mode == "release" {
		return r.release(ctx, source)
	}
	if mode == "desktop" {
		return r.desktop(ctx, source)
	}
	// Install system tools before adding source, so edits reuse the apt layer.
	base := client.Container().From(goImage).
		WithMountedCache("/go/pkg/mod", client.CacheVolume("oie-ci-go-mod-v1")).
		WithMountedCache("/root/.cache/go-build", client.CacheVolume("oie-ci-go-build-v1")).
		WithMountedCache("/root/.cache/golangci-lint", client.CacheVolume("oie-ci-golangci-lint-v1")).
		// Bound both Go's runtime and its default package/build parallelism on
		// shared runners. Child processes still keep their production deadlines.
		WithEnvVariable("GOMAXPROCS", "4").
		WithEnvVariable("GOWORK", "off").WithExec([]string{"sh", "-c", setupCommand})
	base, err := r.withLinters(ctx, base)
	if err != nil {
		return err
	}
	base, err = r.stage(ctx, "setup", base)
	if err != nil {
		return err
	}
	docker := client.Container().From(dockerImage)
	// SQLite fixtures create and migrate databases in t.TempDir. Match the
	// development Compose fixture by keeping that scratch I/O off the shared disk.
	const goScratch = "/tmp/oie-go"
	goContainer := base.WithDirectory("/src", source).WithWorkdir("/src").
		WithFile("/usr/local/bin/docker", docker.File("/usr/local/bin/docker")).
		WithDirectory("/usr/local/libexec/docker/cli-plugins", docker.Directory("/usr/local/libexec/docker/cli-plugins")).
		WithMountedTemp(goScratch, dagger.ContainerWithMountedTempOpts{Size: 2 << 30}).
		WithEnvVariable("TMPDIR", goScratch).
		// A cold toolchain build can exceed the test scratch mount capacity.
		WithEnvVariable("GOTMPDIR", "/var/tmp")
	checked, err := r.stage(ctx, "checks", goContainer.WithExec([]string{"make", "check"}))
	if err != nil {
		return err
	}
	checked, err = r.stage(ctx, "race", checked.WithExec([]string{"make", "test-race"}))
	if err != nil {
		return err
	}
	checked, err = r.stage(ctx, "build", checked.WithExec([]string{"make", "build", "build-cli"}))
	if err != nil {
		return err
	}
	checked, err = r.stage(ctx, "mock-build", checked.WithExec([]string{"go", "build", "-o", "bin/oie-mock", "./cmd/oie-mock"}))
	if err != nil {
		return err
	}
	if mode == "checks" {
		return nil
	}
	if err := r.desktop(ctx, source); err != nil {
		return err
	}
	return r.integration(ctx, source, goContainer, checked)
}

func (r runner) integration(ctx context.Context, source *dagger.Directory, goContainer, checked *dagger.Container) (err error) {
	runID := os.Getenv("OIE_CI_RUN_ID")
	if runID == "" {
		return errors.New("OIE_CI_RUN_ID is required for isolated services")
	}
	client := r.client
	const browserFixtures = "OIE_XMPP_BROWSER_FIXTURES"
	const startsDir = "/run/oie-ci-prosody"
	starts := client.CacheVolume("oie-ci-prosody-starts-v1")
	startsFile := filepath.Join(startsDir, runID)
	prosody := client.Container().From(xmppImage).WithoutEntrypoint().
		WithFile("/etc/prosody/prosody.cfg.lua", source.File("dev/xmpp/prosody.cfg.lua")).
		WithFile("/opt/oie/start.sh", source.File("dev/xmpp/start.sh")).
		WithEnvVariable("OIE_CI_RUN_ID", runID).
		WithEnvVariable(browserFixtures, "1").
		WithExec([]string{"sh", "-c", "mkdir -p /var/lib/prosody/certs && openssl req -x509 -newkey rsa:2048 -nodes -days 30 -subj /CN=localhost -addext subjectAltName=DNS:localhost,IP:127.0.0.1 -keyout /var/lib/prosody/certs/localhost.key -out /var/lib/prosody/certs/localhost.crt"})
	ca := prosody.File("/var/lib/prosody/certs/localhost.crt")
	for _, port := range []int{80, 443, 5269, 5280, 5281, 5347} {
		prosody = prosody.WithoutExposedPort(port)
	}
	// The receipt lives outside the service filesystem so a restart is counted.
	xmpp := prosody.WithExposedPort(5222).
		WithMountedCache(startsDir, starts).WithEnvVariable("OIE_CI_PROSODY_STARTS_FILE", startsFile).
		AsService(dagger.ContainerAsServiceOpts{Args: []string{"sh", "-ec", `
printf 'started\n' >> "$OIE_CI_PROSODY_STARTS_FILE"
exec sh /opt/oie/start.sh
`}})
	// Hold a service reference across the Go and browser stages. A binding
	// alone can stop Prosody while Playwright is being prepared.
	xmpp, err = xmpp.Start(ctx)
	if err != nil {
		return fmt.Errorf("start integration Prosody: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, stopErr := xmpp.Stop(stopCtx); stopErr != nil {
			err = errors.Join(err, fmt.Errorf("stop integration Prosody: %w", stopErr))
		}
	}()
	integrated := goContainer.WithServiceBinding("xmpp", xmpp).WithFile("/tmp/xmpp-ca.crt", ca).
		WithEnvVariable("OIE_XMPP_INTEGRATION", "1").WithEnvVariable("OIE_XMPP_CA_FILE", "/tmp/xmpp-ca.crt").
		WithEnvVariable("OIE_XMPP_ENDPOINT", "xmpp:5222").WithEnvVariable("OIE_CI_RUN_ID", runID).
		WithExec([]string{"sh", "-ec", `
python3 scripts/test-xmpp.py --host xmpp --ca-file "$OIE_XMPP_CA_FILE"
exec go test -race -count=1 -p 1 -v ./tests/xmpp ./tests/cli
`})
	browser := client.Container().From(nodeImage).
		WithMountedCache("/root/.npm", client.CacheVolume("oie-ci-npm-v1")).
		WithMountedCache("/root/.cache/ms-playwright", client.CacheVolume("oie-ci-playwright-1.62.1")).
		WithWorkdir("/src/tests/browser").
		WithFile("package.json", source.File("tests/browser/package.json")).
		WithFile("package-lock.json", source.File("tests/browser/package-lock.json")).
		WithExec([]string{"npm", "ci"}).WithExec([]string{"npx", "playwright", "install", "--with-deps", "chromium"})
	xmppResult := make(chan error, 1)
	go func() {
		_, err := r.stage(ctx, "xmpp", integrated)
		xmppResult <- err
	}()
	browser, browserSetupErr := r.stage(ctx, "browser-setup", browser)
	if err := errors.Join(<-xmppResult, browserSetupErr); err != nil {
		return err
	}
	const browserScratch = "/tmp/oie-browser"
	browser = browser.WithDirectory("/src", source).
		WithFile("/src/bin/oie", checked.File("/src/bin/oie")).WithWorkdir("/src").
		WithMountedTemp(browserScratch, dagger.ContainerWithMountedTempOpts{Size: 2 << 30}).
		WithEnvVariable("TMPDIR", browserScratch).
		WithEnvVariable("OIE_CI_RUN_ID", runID)
	consumer := browser.WithFile("/src/bin/oie-mock", checked.File("/src/bin/oie-mock")).
		WithExec([]string{"sh", "-c", browserCommand, "sh", "consumer", "consumer.log"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	browser = browser.
		WithFile("/tmp/xmpp-ca.crt", ca).WithServiceBinding("xmpp", xmpp).
		WithMountedCache(startsDir, starts).WithEnvVariable("OIE_CI_PROSODY_STARTS_FILE", startsFile).
		WithEnvVariable("OIE_XMPP_CA_FILE", "/tmp/xmpp-ca.crt").WithEnvVariable("OIE_XMPP_ENDPOINT", "xmpp:5222").
		WithEnvVariable(browserFixtures, "1").
		WithExec([]string{"sh", "-c", browserCommand, "sh", "integration", "ci.log"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny})
	// The consumer fixtures need neither the XMPP binding nor the live browser's
	// loopback ports. Keep each group's processes and filesystem in its own container.
	type browserResult struct {
		container *dagger.Container
		err       error
	}
	consumerResult := make(chan browserResult, 1)
	go func() {
		evaluated, err := r.stage(ctx, "browser-consumer", consumer)
		consumerResult <- browserResult{evaluated, err}
	}()
	browser, liveErr := r.stage(ctx, "browser", browser)
	result := <-consumerResult
	// Merge the disjoint evidence files sequentially on the host.
	if result.err == nil {
		result.err = r.exportBrowserEvidence(ctx, result.container, "consumer.log")
	}
	if liveErr == nil {
		liveErr = r.exportBrowserEvidence(ctx, browser, "ci.log")
	}
	return errors.Join(result.err, liveErr)
}

func (r runner) exportBrowserEvidence(ctx context.Context, browser *dagger.Container, logName string) error {
	// Export evidence before deciding whether the workflow failed. A failure can
	// never become a passing CI just because the export succeeded.
	if _, err := browser.Directory("/src/tests/browser/artifacts").Export(ctx, filepath.Join(r.logs, "browser")); err != nil {
		return fmt.Errorf("export browser evidence for %s: %w", logName, err)
	}
	code, err := browser.File("/tmp/oie-browser-exit").Contents(ctx)
	if err != nil {
		return err
	}
	if err := browserOutcome(code); err != nil {
		return fmt.Errorf("%w; see exported browser/%s", err, logName)
	}
	return nil
}

const browserCommand = `
mkdir -p tests/browser/artifacts
log="tests/browser/artifacts/$2"
sh tools/ci/browser.sh "$1" > "$log" 2>&1
result=$?
if [ "$1" = integration ]; then
  starts=0
  starts_file="${OIE_CI_PROSODY_STARTS_FILE:-}"
  if [ -f "$starts_file" ]; then
    starts=$(wc -l < "$starts_file")
    rm -f "$starts_file"
  fi
  printf 'Prosody starts: %s (expected 1)\n' "$starts" >> "$log"
  if [ "$starts" != 1 ] && [ "$result" = 0 ]; then result=1; fi
fi
printf '%s\n' "$result" > /tmp/oie-browser-exit
cat "$log"
exit "$result"
`

func browserOutcome(code string) error {
	if strings.TrimSpace(code) != "0" {
		return fmt.Errorf("browser workflow failed with exit %q", strings.TrimSpace(code))
	}
	return nil
}
