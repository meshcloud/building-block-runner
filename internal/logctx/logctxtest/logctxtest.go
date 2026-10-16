// Package logctxtest records the slog records that a test logs with its context.
package logctxtest

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/meshcloud/building-block-runner/internal/logctx"
)

// Install drops every record that no recorder takes. Call it once, from TestMain.
func Install() {
	slog.SetDefault(slog.New(logctx.NewHandler(slog.DiscardHandler)))
}

// Record returns a context whose records Lines gets, as text without time and level.
func Record(t *testing.T) (context.Context, *Lines) {
	t.Helper()
	lines := &Lines{}
	recorder := slog.NewTextHandler(lines, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: withoutTimeAndLevel})
	return logctx.WithRecorder(t.Context(), recorder), lines
}

func withoutTimeAndLevel(groups []string, attr slog.Attr) slog.Attr {
	if len(groups) == 0 && (attr.Key == slog.TimeKey || attr.Key == slog.LevelKey) {
		return slog.Attr{}
	}
	return attr
}

type Lines struct {
	mu   sync.Mutex
	text strings.Builder
}

func (l *Lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text.Write(p)
}

func (l *Lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text.String()
}

func (l *Lines) Lines() []string {
	return strings.Split(strings.TrimSuffix(l.String(), "\n"), "\n")
}
