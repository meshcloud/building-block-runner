package meshapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDownloadArtifact_StreamsBodyIntoWriter(t *testing.T) {
	payload := []byte("a saved terraform plan, possibly quite large")

	var gotAccept, gotContentType, gotNodeID, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		gotNodeID = r.Header.Get("X-Block-Runner-Node-Id")
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		w.Write(payload)
	}))
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, "test-node", BearerTokenAuth{Token: "run-token"}, srv.Client())

	var buf bytes.Buffer
	err := client.DownloadArtifact(srv.URL+"/artifact", &buf)

	require.NoError(t, err)
	assert.Equal(t, payload, buf.Bytes(), "body should be streamed verbatim into the writer")
	assert.Equal(t, "application/octet-stream", gotAccept, "should request raw bytes, not HAL+JSON")
	assert.Empty(t, gotContentType, "Content-Type should be removed for a bodyless GET")
	assert.Equal(t, "test-node", gotNodeID, "standard runner headers should still be sent")
	assert.Equal(t, "Bearer run-token", gotAuth, "run auth should still be applied")
}

func TestDownloadArtifact_RejectsCrossOriginURL(t *testing.T) {
	// The artifact host must match the client's configured baseURL, otherwise the run bearer token
	// attached by setHeaders would leak to a foreign host. The request must never be issued.
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClientWithHTTP("https://api.example.com", "test-node", BearerTokenAuth{Token: "run-token"}, srv.Client())

	var buf bytes.Buffer
	err := client.DownloadArtifact(srv.URL+"/artifact", &buf)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to send authenticated request")
	assert.False(t, reached, "the cross-origin request must not be issued")
	assert.Empty(t, buf.Bytes())
}

func TestDownloadArtifact_Non2xxReturnsErrorWithBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "artifact expired", http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewClientWithHTTP(srv.URL, "test-node", BearerTokenAuth{Token: "run-token"}, srv.Client())

	var buf bytes.Buffer
	err := client.DownloadArtifact(srv.URL+"/artifact", &buf)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "artifact expired", "non-2xx body should surface to the caller")
	assert.Empty(t, buf.Bytes(), "nothing should be written on a non-2xx response")
}
