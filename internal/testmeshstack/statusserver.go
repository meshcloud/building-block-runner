package testmeshstack

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/meshcloud/meshstack-cli/client"
)

type StatusServer struct {
	URL             string
	PatchStatusCode int
}

func NewStatusServer(t *testing.T) *StatusServer {
	t.Helper()
	s := &StatusServer{PatchStatusCode: http.StatusOK}
	server := httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(server.Close)
	s.URL = server.URL
	return s
}

func (s *StatusServer) Link(run *client.MeshRunnerRun) {
	run.Spec.RunToken = Token
	run.Links.MeshstackBaseUrl.Href = s.URL
	run.Links.RegisterSource.Href = s.URL + "/source"
	run.Links.UpdateSource.Href = s.URL + "/source/{sourceId}"
}

func (s *StatusServer) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch && s.PatchStatusCode != http.StatusOK {
		w.WriteHeader(s.PatchStatusCode)
		return
	}
	_, _ = w.Write([]byte(`{}`))
}
