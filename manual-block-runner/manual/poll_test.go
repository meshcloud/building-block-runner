package manual

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_StoppedPoller_ClaimsNoRun(t *testing.T) {
	var requests atomic.Int32
	meshStack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer meshStack.Close()

	cfg := defaultConfig()
	cfg.Api.Url = meshStack.URL
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	NewPoller(cfg).Run(ctx)

	assert.Zero(t, requests.Load())
}
