// Package logctx carries log attributes and a recording handler in a context, so that every
// record logged with that context gets them.
package logctx

import (
	"context"
	"errors"
	"log/slog"
	"slices"
)

type attrsKey struct{}

type recorderKey struct{}

// With replaces an attribute that ctx already carries under the same key.
func With(ctx context.Context, attrs ...slog.Attr) context.Context {
	carried := slices.DeleteFunc(slices.Clone(attrsOf(ctx)), func(c slog.Attr) bool {
		return slices.ContainsFunc(attrs, func(a slog.Attr) bool { return a.Key == c.Key })
	})
	return context.WithValue(ctx, attrsKey{}, append(carried, attrs...))
}

// WithRecorder passes every record logged with ctx to recorder as well, so that parallel tests
// can each record the runner they started.
func WithRecorder(ctx context.Context, recorder slog.Handler) context.Context {
	return context.WithValue(ctx, recorderKey{}, recorder)
}

func attrsOf(ctx context.Context) []slog.Attr {
	attrs, _ := ctx.Value(attrsKey{}).([]slog.Attr)
	return attrs
}

func recorderOf(ctx context.Context) slog.Handler {
	recorder, _ := ctx.Value(recorderKey{}).(slog.Handler)
	return recorder
}

type handler struct {
	next slog.Handler
	// derive replays WithAttrs and WithGroup on the recorder, which only Handle knows.
	derive []func(slog.Handler) slog.Handler
}

// NewHandler adds the attributes of the context to every record, and passes it to the recorder of the context too.
func NewHandler(next slog.Handler) slog.Handler {
	return handler{next: next}
}

func (h handler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.next.Enabled(ctx, level) {
		return true
	}
	recorder := recorderOf(ctx)
	return recorder != nil && recorder.Enabled(ctx, level)
}

func (h handler) Handle(ctx context.Context, record slog.Record) error {
	record = record.Clone()
	record.AddAttrs(attrsOf(ctx)...)
	var nextErr, recorderErr error
	if h.next.Enabled(ctx, record.Level) {
		nextErr = h.next.Handle(ctx, record)
	}
	if recorder := recorderOf(ctx); recorder != nil {
		for _, derive := range h.derive {
			recorder = derive(recorder)
		}
		if recorder.Enabled(ctx, record.Level) {
			recorderErr = recorder.Handle(ctx, record)
		}
	}
	return errors.Join(nextErr, recorderErr)
}

func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.with(func(next slog.Handler) slog.Handler { return next.WithAttrs(attrs) })
}

func (h handler) WithGroup(name string) slog.Handler {
	return h.with(func(next slog.Handler) slog.Handler { return next.WithGroup(name) })
}

func (h handler) with(derive func(slog.Handler) slog.Handler) slog.Handler {
	return handler{next: derive(h.next), derive: append(slices.Clip(h.derive), derive)}
}

// Bind handles every record with the attributes and the recorder of ctx, for a logger that passes
// no context, such as one from slog.NewLogLogger.
func Bind(ctx context.Context, next slog.Handler) slog.Handler {
	return bound{attrs: attrsOf(ctx), recorder: recorderOf(ctx), next: next}
}

type bound struct {
	attrs    []slog.Attr
	recorder slog.Handler
	next     slog.Handler
}

func (b bound) Enabled(ctx context.Context, level slog.Level) bool {
	return b.next.Enabled(b.into(ctx), level)
}

func (b bound) Handle(ctx context.Context, record slog.Record) error {
	return b.next.Handle(b.into(ctx), record)
}

func (b bound) WithAttrs(attrs []slog.Attr) slog.Handler {
	return bound{attrs: b.attrs, recorder: b.recorder, next: b.next.WithAttrs(attrs)}
}

func (b bound) WithGroup(name string) slog.Handler {
	return bound{attrs: b.attrs, recorder: b.recorder, next: b.next.WithGroup(name)}
}

func (b bound) into(ctx context.Context) context.Context {
	ctx = With(ctx, b.attrs...)
	if b.recorder != nil {
		ctx = WithRecorder(ctx, b.recorder)
	}
	return ctx
}
