package main

import (
	"io"
	"log"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_MetricsPortTaken_FailsToServe(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })

	assert.ErrorContains(t, serveMetrics(log.New(io.Discard, "", 0), taken.Addr().String()), "address already in use")
}
