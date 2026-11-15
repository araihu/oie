// Package diagnostics records privacy-safe operational events for local sharing.
package diagnostics

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

type Event string

const (
	Starting          Event = "starting"
	Ready             Event = "ready"
	Stopped           Event = "stopped"
	ConfigFailed      Event = "configuration_failed"
	ListenFailed      Event = "listen_failed"
	StorageFailed     Event = "storage_startup_failed"
	EngineFailed      Event = "engine_startup_failed"
	WorkerFailed      Event = "engine_worker_failed"
	ShutdownFailed    Event = "shutdown_failed"
	DesktopFailed     Event = "desktop_startup_failed"
	PreferencesFailed Event = "preferences_write_failed"
	ResetFailed       Event = "engine_reset_failed"
	RetentionFailed   Event = "history_expiry_failed"
	IndexUnavailable  Event = "search_index_unavailable"
	EngineLocal       Event = "local_engine_selected"
	EngineRemote      Event = "remote_engine_selected"
	EngineSwitchError Event = "engine_switch_failed"
	EngineHTTPError   Event = "engine_http_error"
	UpstreamHTTPError Event = "upstream_http_error"
	EngineTimeout     Event = "engine_http_timeout"
	EngineUnavailable Event = "engine_http_unavailable"
	XMPPConnected     Event = "xmpp_connected"
	XMPPDisconnected  Event = "xmpp_disconnected"
	XMPPDNSFailed     Event = "xmpp_dns_failed"
	XMPPTLSFailed     Event = "xmpp_tls_failed"
	XMPPAuthFailed    Event = "xmpp_authentication_failed"
	XMPPTimeout       Event = "xmpp_connect_timeout"
	XMPPConnectFailed Event = "xmpp_connect_failed"
	SendFailed        Event = "send_failed"
	SendUnknown       Event = "send_outcome_unknown"
	SendDeferred      Event = "send_retry_deferred"
	HistoryIncomplete Event = "history_recovery_incomplete"
	MediaFailed       Event = "media_fetch_failed"
	RuntimeWarning    Event = "runtime_warning"
	LogStorageFailed  Event = "diagnostic_storage_failed"
)

type eventInfo struct{ component, level string }

var events = map[Event]eventInfo{
	Starting: {"host", "info"}, Ready: {"host", "info"}, Stopped: {"host", "info"},
	ConfigFailed: {"host", "error"}, ListenFailed: {"http", "error"}, StorageFailed: {"sqlite", "error"},
	EngineFailed: {"engine", "error"}, WorkerFailed: {"engine", "error"}, ShutdownFailed: {"host", "error"},
	DesktopFailed: {"desktop", "error"}, PreferencesFailed: {"presentation", "warn"}, ResetFailed: {"engine", "error"},
	RetentionFailed: {"sqlite", "error"}, IndexUnavailable: {"sqlite", "warn"},
	EngineLocal: {"desktop", "info"}, EngineRemote: {"desktop", "info"}, EngineSwitchError: {"desktop", "warn"},
	EngineHTTPError: {"engine", "warn"}, UpstreamHTTPError: {"presentation", "warn"}, EngineTimeout: {"http", "warn"}, EngineUnavailable: {"http", "warn"},
	XMPPConnected: {"xmpp", "info"}, XMPPDisconnected: {"xmpp", "warn"},
	XMPPDNSFailed: {"xmpp", "warn"}, XMPPTLSFailed: {"xmpp", "warn"}, XMPPAuthFailed: {"xmpp", "warn"},
	XMPPTimeout: {"xmpp", "warn"}, XMPPConnectFailed: {"xmpp", "warn"},
	SendFailed: {"outbox", "warn"}, SendUnknown: {"outbox", "warn"}, SendDeferred: {"outbox", "debug"},
	HistoryIncomplete: {"history", "warn"}, MediaFailed: {"media", "warn"},
	RuntimeWarning: {"runtime", "warn"}, LogStorageFailed: {"diagnostics", "warn"},
}

type Mode string

const (
	Desktop Mode = "desktop"
	Local   Mode = "local"
	Hosted  Mode = "hosted"
	BFF     Mode = "bff"
)

func (m Mode) valid() bool { return m == Desktop || m == Local || m == Hosted || m == BFF }

// buildVersion can be set by the release build. Source builds can use the
// revision Go embeds. Never emit arbitrary build metadata.
var buildVersion string
var versionPattern = regexp.MustCompile(`^v[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}(-rc\.[0-9]{1,5})?$`)
var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func version() string {
	if versionPattern.MatchString(buildVersion) || revisionPattern.MatchString(buildVersion) {
		return buildVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if versionPattern.MatchString(info.Main.Version) {
			return info.Main.Version
		}
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && revisionPattern.MatchString(setting.Value) {
				return setting.Value
			}
		}
	}
	return "development"
}

type record struct {
	Time       time.Time `json:"time"`
	Level      string    `json:"level"`
	Component  string    `json:"component"`
	Event      Event     `json:"event"`
	Build      string    `json:"build"`
	Platform   string    `json:"platform"`
	Mode       Mode      `json:"mode"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	Attempt    int       `json:"attempt,omitempty"`
	Status     int       `json:"http_status,omitempty"`
	RequestRef string    `json:"request_ref,omitempty"`
}

type frequency struct {
	since time.Time
	count int
}

type Logger struct {
	mu         sync.Mutex
	dir        string
	output     io.Writer
	mode       Mode
	debug      bool
	build      string
	now        func() time.Time
	recent     map[Event]frequency
	fileFailed bool
}

func New(dir string, output io.Writer, mode Mode, debug bool) *Logger {
	if !mode.valid() {
		mode = Local
	}
	return &Logger{dir: dir, output: output, mode: mode, debug: debug, build: version(), now: time.Now, recent: make(map[Event]frequency)}
}

// Record only accepts known events and numeric observation fields. In
// particular there is no error, message, attribute map, or identifier input.
func (l *Logger) Record(event Event, elapsed time.Duration, attempt int) {
	l.record(event, elapsed, attempt, 0, "")
}

// ReportSendOutcome observes the engine's finite set of outbox outcomes.
func (l *Logger) ReportSendOutcome(state string, elapsed time.Duration) {
	switch state {
	case "queued":
		l.Record(SendDeferred, elapsed, 0)
	case "failed":
		l.Record(SendFailed, elapsed, 0)
	case "unknown":
		l.Record(SendUnknown, elapsed, 0)
	}
}

func (l *Logger) ReportMediaFailure()     { l.Record(MediaFailed, 0, 0) }
func (l *Logger) ReportRetentionFailure() { l.Record(RetentionFailed, 0, 0) }

var tracePattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var requestRefPattern = regexp.MustCompile(`^[a-f0-9]{24}$`)

// RecordHTTP keeps only the error status and a one-way reference to an opaque
// request trace. A trace from an external client is never stored verbatim.
func (l *Logger) RecordHTTP(event Event, elapsed time.Duration, status int, trace string) {
	switch event {
	case EngineHTTPError, UpstreamHTTPError:
		if status < 500 || status > 599 {
			return
		}
	case EngineTimeout, EngineUnavailable:
		status = 0
	default:
		return
	}
	var ref string
	if tracePattern.MatchString(trace) {
		hash := sha256.Sum256([]byte("oie diagnostic request " + trace))
		ref = fmt.Sprintf("%x", hash[:12])
	}
	l.record(event, elapsed, 0, status, ref)
}

func (l *Logger) record(event Event, elapsed time.Duration, attempt, status int, ref string) {
	if l == nil {
		return
	}
	meta, known := events[event]
	if !known || (meta.level == "debug" && !l.debug) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now().UTC()
	rate := l.recent[event]
	if now.Sub(rate.since) >= time.Minute || now.Before(rate.since) {
		rate = frequency{since: now}
	}
	if rate.count >= 5 {
		return
	}
	rate.count++
	l.recent[event] = rate
	value := record{
		Time: now, Level: meta.level, Component: meta.component, Event: event,
		Build: l.build, Platform: runtime.GOOS, Mode: l.mode,
		DurationMS: min(max(0, elapsed.Milliseconds()), int64(24*time.Hour/time.Millisecond)),
		Attempt:    min(max(0, attempt), 999), Status: status, RequestRef: ref,
	}
	line, err := json.Marshal(value)
	if err != nil {
		return
	}
	line = append(line, '\n')
	l.writeOutput(line)
	if l.dir == "" {
		return
	}
	if err := l.appendFile(now, line); err != nil && !l.fileFailed {
		l.fileFailed = true
		value.Event, value.Level, value.Component = LogStorageFailed, "warn", "diagnostics"
		value.DurationMS, value.Attempt, value.Status, value.RequestRef = 0, 0, 0, ""
		line, err = json.Marshal(value)
		if err == nil {
			l.writeOutput(append(line, '\n'))
		}
	}
}

func (l *Logger) writeOutput(line []byte) {
	if l.output != nil {
		_, _ = l.output.Write(line)
	}
}

// StandardLogger absorbs text from library logging, including net/http panic
// messages. Only the fact that a warning occurred is recorded.
func (l *Logger) StandardLogger() *log.Logger { return log.New(warningWriter{l}, "", 0) }

type warningWriter struct{ log *Logger }

func (w warningWriter) Write(b []byte) (int, error) {
	w.log.Record(RuntimeWarning, 0, 0)
	return len(b), nil
}

func UseStandardLogger(l *Logger) { log.SetOutput(warningWriter{l}) }

func platformValid(value string) bool {
	return value == "linux" || value == "darwin" || value == runtime.GOOS
}

func IsDebug() bool { return os.Getenv("OIE_LOG_LEVEL") == "debug" }
