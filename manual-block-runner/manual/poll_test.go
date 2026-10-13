package manual

import (
	"context"
	gohttp "net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
)

func Test_StoppedPoller_ClaimsNoRun(t *testing.T) {
	var requests atomic.Int32
	meshStack := httptest.NewServer(gohttp.HandlerFunc(func(w gohttp.ResponseWriter, r *gohttp.Request) {
		requests.Add(1)
		w.WriteHeader(gohttp.StatusNotFound)
	}))
	defer meshStack.Close()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	NewPoller(runnerClient(t, meshStack.URL), uuid.New()).Run(ctx)

	assert.Zero(t, requests.Load())
}
