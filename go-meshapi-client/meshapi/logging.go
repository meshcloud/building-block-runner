package meshapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
)

// Logger is the pluggable request/response log seam of the client. Its shape is copied
// verbatim from the terraform-provider-meshstack client's internal.Logger (ctx-first,
// slog-shaped) so the future shared meshstack-go-sdk merge (D3, §7) has no logging-seam
// delta. The default is noopLogger; wire a real one with WithLogger(SlogLogger(l)).
//
// At Debug the client logs the request ("method","url","headers","body") and response
// ("status","body"); the header/body values are lazy fmt.Stringers (loggedHeaders /
// loggedBody) so they cost nothing unless the handler actually renders them.
type Logger interface {
	Debug(ctx context.Context, msg string, args ...any)
	Info(ctx context.Context, msg string, args ...any)
	Warn(ctx context.Context, msg string, args ...any)
}

// noopLogger is the default: it discards everything, so an un-wired client logs nothing.
type noopLogger struct{}

func (noopLogger) Debug(context.Context, string, ...any) {}
func (noopLogger) Info(context.Context, string, ...any)  {}
func (noopLogger) Warn(context.Context, string, ...any)  {}

// slogAdapter adapts the app's *slog.Logger to the Logger seam. The interface signature
// already matches slog, so the adapter just forwards to the *Context variants (level
// gating via LOG_LEVEL happens in the slog handler — no separate Enabled() check, §5.2.6).
type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Debug(ctx context.Context, msg string, args ...any) {
	a.l.DebugContext(ctx, msg, args...)
}
func (a slogAdapter) Info(ctx context.Context, msg string, args ...any) {
	a.l.InfoContext(ctx, msg, args...)
}
func (a slogAdapter) Warn(ctx context.Context, msg string, args ...any) {
	a.l.WarnContext(ctx, msg, args...)
}

// SlogLogger adapts a *slog.Logger to the client's Logger seam. A nil logger yields the
// noop logger so callers need not guard.
func SlogLogger(l *slog.Logger) Logger {
	if l == nil {
		return noopLogger{}
	}
	return slogAdapter{l: l}
}

// loggedHeaders is a lazy fmt.Stringer over request headers. It renders keys sorted for
// stable output and masks the Authorization value as [REDACTED] — the single sanctioned
// exception to the "don't obfuscate logs" policy, copied from the provider client
// (§5.2.6). Every other header (and every body) is logged in full at Debug.
type loggedHeaders http.Header

func (h loggedHeaders) String() string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		if strings.EqualFold(k, "Authorization") {
			b.WriteString(k + ": [REDACTED]")
			continue
		}
		b.WriteString(k + ": " + strings.Join(h[k], ","))
	}
	return b.String()
}

// loggedBody is a lazy fmt.Stringer over a (buffered) request/response body. Only buffered
// JSON payloads are ever wrapped in it — DownloadArtifact's up-to-128MiB stream is never
// routed through it (§5.2.6), so Debug logging cannot exhaust memory on a large artifact.
type loggedBody []byte

func (b loggedBody) String() string { return bytesToPrettyJson(b) }

// bytesToPrettyJson pretty-prints JSON bytes, falling back to the raw string when the
// bytes are not valid JSON. Copied from the provider client's logging helper.
func bytesToPrettyJson(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, b, "", "  "); err != nil {
		return string(b)
	}
	return pretty.String()
}

var _ fmt.Stringer = loggedHeaders(nil)
var _ fmt.Stringer = loggedBody(nil)
