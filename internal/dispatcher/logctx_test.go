package dispatcher

import (
	"bytes"
	"log/slog"
	"testing"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
)

func Test_WithRun_AddsTheRunToEveryRecord(t *testing.T) {
	var out bytes.Buffer
	logger := slog.New(NewLogHandler(slog.NewTextHandler(&out, &slog.HandlerOptions{ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
		if len(groups) == 0 && attr.Key == slog.TimeKey {
			return slog.Attr{}
		}
		return attr
	}})))
	runUuid := uuid.New()
	ctx := WithRun(t.Context(), Run{Metadata: client.MeshRunnerRunMetadata{Uuid: runUuid}})

	logger.With("dispatcher", "k8s").InfoContext(ctx, "created job")
	logger.InfoContext(t.Context(), "claiming")

	assert.Equal(t, "level=INFO msg=\"created job\" dispatcher=k8s run="+runUuid.String()+"\n"+
		"level=INFO msg=claiming\n", out.String())
}
