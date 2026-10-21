package dispatcher

import (
	"context"
	"log/slog"
	"uuid"
)

type runKey struct{}

func WithRun(ctx context.Context, run Run) context.Context {
	return context.WithValue(ctx, runKey{}, run.Metadata.Uuid)
}

type logHandler struct {
	slog.Handler
}

func NewLogHandler(next slog.Handler) slog.Handler {
	return logHandler{next}
}

func (h logHandler) Handle(ctx context.Context, record slog.Record) error {
	if runUuid, ok := ctx.Value(runKey{}).(uuid.UUID); ok {
		record = record.Clone()
		record.AddAttrs(slog.Any("run", runUuid))
	}
	return h.Handler.Handle(ctx, record)
}

func (h logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return logHandler{h.Handler.WithAttrs(attrs)}
}

func (h logHandler) WithGroup(name string) slog.Handler {
	return logHandler{h.Handler.WithGroup(name)}
}
