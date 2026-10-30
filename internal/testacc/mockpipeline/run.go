package mockpipeline

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/meshcloud/meshstack-cli/client"

	"github.com/meshcloud/building-block-runner/v2/internal/executor"
)

type Run struct {
	Id       int64
	Created  time.Time
	Polls    int
	Canceled bool

	outcome executor.Status
}

// Progress picks the status of a run by its polls from statuses, the last of which completes it. A pipeline
// without an outcome completes only when canceled.
func (r Run) Progress(statuses []string) (status string, completed bool) {
	last := len(statuses) - 1
	if r.Canceled {
		return statuses[last], true
	}
	if r.outcome == "" {
		last--
	}
	stage := min(r.Polls, last)
	return statuses[stage], stage == len(statuses)-1
}

func (r Run) EndsCanceled() bool {
	return r.Canceled || r.outcome == executor.StatusAborted
}

func (r Run) Succeeds() bool {
	return r.outcome == executor.StatusSucceeded
}

func (p *Pipeline) Poll(id int64) (Run, bool) {
	return p.update(id, func(run *Run) { run.Polls++ })
}

func (p *Pipeline) Peek(id int64) (Run, bool) {
	return p.update(id, func(*Run) {})
}

func (p *Pipeline) Cancel(id int64) (Run, bool) {
	return p.update(id, func(run *Run) { run.Canceled = true })
}

func (p *Pipeline) update(id int64, change func(*Run)) (Run, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id < 1 || id > int64(len(p.runs)) {
		return Run{}, false
	}
	change(&p.runs[id-1])
	return p.runs[id-1], true
}

func (p *Pipeline) NewestRuns(yield func(Run) bool) {
	p.mu.Lock()
	runs := slices.Clone(p.runs)
	p.mu.Unlock()
	for _, run := range slices.Backward(runs) {
		if !yield(run) {
			return
		}
	}
}

// RunAt is the run as a pipeline sees it that got only the run URL and token.
func RunAt(runUrl, runToken string) (client.MeshRunnerRun, error) {
	if runUrl == "" {
		return client.MeshRunnerRun{}, errors.New("the trigger carried no run URL")
	}
	return client.MeshRunnerRun{
		Spec: client.MeshRunnerRunSpec{RunToken: runToken},
		Links: client.MeshRunnerRunLinks{
			Self:           client.MeshRunnerRunLink{Href: runUrl},
			RegisterSource: client.MeshRunnerRunLink{Href: runUrl + "/status/source"},
			UpdateSource:   client.MeshRunnerRunLink{Href: runUrl + "/status/source/{sourceId}", Templated: true},
		},
	}, nil
}

func DecodeRun(encoded []byte) (run client.MeshRunnerRun, err error) {
	if err = json.Unmarshal(encoded, &run); err != nil {
		err = fmt.Errorf("decode the run: %w", err)
	}
	return
}

func DecodeBody[T any](w http.ResponseWriter, r *http.Request, message string) (T, bool) {
	var body T
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		WriteJson(w, http.StatusBadRequest, map[string]any{"message": message})
		return body, false
	}
	return body, true
}

func WriteJson(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, body)
}
