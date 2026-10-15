package logctx

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

type lines struct {
	mu      sync.Mutex
	builder strings.Builder
}

func (l *lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.builder.Write(p)
}

func (l *lines) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.builder.String()
}

func textHandler(level slog.Level) (slog.Handler, *lines) {
	var out lines
	return slog.NewTextHandler(&out, &slog.HandlerOptions{Level: level, ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
		if len(groups) == 0 && attr.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return attr
	}}), &out
}

func Test_With_AddsTheAttrsToEveryRecord(t *testing.T) {
	next, out := textHandler(slog.LevelInfo)
	logger := slog.New(NewHandler(next))
	ctx := With(t.Context(), slog.String("runner", "r1"), slog.String("run", "a"))

	logger.InfoContext(With(ctx, slog.String("run", "b")), "claimed run")
	logger.With("dispatcher", "k8s").WarnContext(ctx, "created job")

	assert.Equal(t, "level=INFO msg=\"claimed run\" runner=r1 run=b\n"+
		"level=WARN msg=\"created job\" dispatcher=k8s runner=r1 run=a\n", out.String())
}

func Test_WithRecorder_KeepsTheRecordsOfTwoContextsApart(t *testing.T) {
	next, out := textHandler(slog.LevelInfo)
	logger := slog.New(NewHandler(next)).With("dispatcher", "k8s")
	recorderA, recordedA := textHandler(slog.LevelDebug)
	recorderB, recordedB := textHandler(slog.LevelInfo)
	ctxA := WithRecorder(With(t.Context(), slog.String("run", "a")), recorderA)
	ctxB := WithRecorder(t.Context(), recorderB)

	var wg sync.WaitGroup
	wg.Go(func() { logger.DebugContext(ctxA, "from a", "n", 1) })
	wg.Go(func() { logger.InfoContext(ctxB, "from b", "n", 2) })
	wg.Go(func() { logger.InfoContext(context.Background(), "from neither") })
	wg.Wait()

	assert.Equal(t, "level=DEBUG msg=\"from a\" dispatcher=k8s n=1 run=a\n", recordedA.String())
	assert.Equal(t, "level=INFO msg=\"from b\" dispatcher=k8s n=2\n", recordedB.String())
	assert.NotContains(t, out.String(), "from a")
	assert.Contains(t, out.String(), "from b")
	assert.Contains(t, out.String(), "from neither")
}

func Test_Bind_HandlesWithTheBoundContext(t *testing.T) {
	recorder, recorded := textHandler(slog.LevelInfo)
	ctx := WithRecorder(With(t.Context(), slog.String("run", "a")), recorder)
	next, _ := textHandler(slog.LevelInfo)

	slog.NewLogLogger(Bind(ctx, NewHandler(next)), slog.LevelInfo).Print("tf output")

	assert.Equal(t, "level=INFO msg=\"tf output\" run=a\n", recorded.String())
}
