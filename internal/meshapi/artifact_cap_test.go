package meshapi

// CP4 (PLAN_DETAIL_01_tf_characterization_tests.md §5 D9 pin 8, §9): pins the 128MiB
// plan-artifact download cap (client.go:20 maxArtifactBytes, client.go:153-159). The former
// same-origin check that used to accompany this cap was deliberately reverted in commit 88d67d4
// and is NOT pinned (see plan §3 F2) — pinning a deleted feature would be impossible.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lazyZeroReader yields exactly Remaining zero bytes without ever allocating them as one big
// slice, so pinning a 128MiB+1-byte transfer doesn't require holding that much memory twice
// (once to build the fake body, once to buffer/discard it).
type lazyZeroReader struct {
	Remaining int64
}

func (r *lazyZeroReader) Read(p []byte) (int, error) {
	if r.Remaining <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > r.Remaining {
		n = r.Remaining
	}
	clear(p[:n])
	r.Remaining -= n
	return int(n), nil
}

// TestDownloadArtifact_OversizedArtifact_RejectedWithMaxSizeError pins that a streamed artifact
// exceeding maxArtifactBytes is rejected with an actionable "exceeds the maximum allowed size"
// error, and that the reader is bounded at maxArtifactBytes+1 (one byte over the cap is enough to
// detect an oversized artifact without reading it in full).
func TestDownloadArtifact_OversizedArtifact_RejectedWithMaxSizeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := io.Copy(w, &lazyZeroReader{Remaining: maxArtifactBytes + 1})
		assert.NoError(t, err)
	}))
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, "test-node", BearerTokenAuth{Token: "run-token"}, srv.Client())

	err := client.DownloadArtifact(srv.URL+"/artifact", io.Discard)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds the maximum allowed size")
}

// TestDownloadArtifact_ArtifactExactlyAtCap_Succeeds pins the boundary: an artifact of exactly
// maxArtifactBytes (not one byte more) is accepted, proving the cap rejects only bodies that
// actually exceed the limit.
func TestDownloadArtifact_ArtifactExactlyAtCap_Succeeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := io.Copy(w, &lazyZeroReader{Remaining: maxArtifactBytes})
		assert.NoError(t, err)
	}))
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, "test-node", BearerTokenAuth{Token: "run-token"}, srv.Client())

	err := client.DownloadArtifact(srv.URL+"/artifact", io.Discard)

	require.NoError(t, err)
}
