package report

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// RunPatcher is the run-scoped subset of *meshapi.RunClient the event-driven reporter
// needs, declared on the consumer side so the reporter is fakeable without HTTP and
// so report keeps depending only on the two calls it actually makes. *meshapi.RunClient
// satisfies it directly.
type RunPatcher interface {
	RegisterSource(runID string, registration meshapi.RegistrationDTO) error
	PatchStatus(runID, sourceID string, payload any) ([]byte, error)
}

// pendingStatus is the register-time step status the Kotlin runners send verbatim
// (HttpBlockRunClient.registerAsSource → MeshBuildingBlockRun defaults).
const pendingStatus = "PENDING"

// eventReporter is the event-driven implementation of the unified Reporter port used by
// the four Kotlin-port runners: Register once, then Report on
// state changes only. It runs NO ticker (that is tf's Observer) and its callers DISCARD
// the abort return (Kotlin never honored the abort flag — HttpBlockRunClient.kt:62-66).
//
// It is deliberately STATELESS: it holds no accumulated step history. The Kotlin runners
// re-send only what changed (ado stage dedup, github job batches, gitlab's single trigger
// step) and that dedup lives in the handlers, so tracking sent steps here would be
// speculative. Each Report sends exactly the steps present in the passed RunStatus.
type eventReporter struct {
	rc       RunPatcher
	sourceId string
	log      *slog.Logger
}

// NewReporter builds the run-scoped event-driven reporter. sourceId is the reporting
// runner's uuid (the status-source id, substituted for the endpoint's {sourceId}); the
// run id travels on each RunStatus. rc is a run-scoped patcher (runToken-only auth
// underneath, built by the runner type per run). A nil log falls back to slog.Default().
func NewReporter(rc RunPatcher, sourceId string, log *slog.Logger) Reporter {
	if log == nil {
		log = slog.Default()
	}
	return &eventReporter{rc: rc, sourceId: sourceId, log: log}
}

// Register registers the reporter as a status source with one step per RunStatus.Steps
// entry, each PENDING (the frozen register body). A 409 (already registered) is
// treated as success inside meshapi.RunClient, so it surfaces here as a nil error.
func (r *eventReporter) Register(s RunStatus) error {
	if s.RunId == "" {
		return fmt.Errorf("run status has no RunId, cannot register a status source")
	}

	steps := make([]meshapi.StepRegistrationDTO, 0, len(s.Steps))
	for _, step := range s.Steps {
		pending := pendingStatus
		steps = append(steps, meshapi.StepRegistrationDTO{
			Id:          step.Name,
			DisplayName: step.DisplayName,
			Status:      &pending,
		})
	}

	registration := meshapi.RegistrationDTO{
		Source: meshapi.SourceDTO{Id: r.sourceId},
		Steps:  steps,
	}

	if err := r.rc.RegisterSource(s.RunId, registration); err != nil {
		return fmt.Errorf("registering as status source for run %s: %w", s.RunId, err)
	}
	return nil
}

// Report PATCHes the lean SourceUpdateDTO for the steps present in s (the changed/new
// steps the caller chose to send). The response body carries the abort flag; it is parsed
// and returned so tf can honor it, but the four runners discard it. A
// malformed/empty response body is not an error — the abort flag simply reads
// false, matching Kotlin ignoring the body entirely.
func (r *eventReporter) Report(s RunStatus) (bool, error) {
	if s.RunId == "" {
		return false, fmt.Errorf("run status has no RunId, cannot report status")
	}

	dto := toSourceUpdate(s)

	data, err := r.rc.PatchStatus(s.RunId, r.sourceId, dto)
	if err != nil {
		return false, fmt.Errorf("reporting status for run %s: %w", s.RunId, err)
	}

	var resp meshapi.RunUpdateResponseDTO
	if len(data) > 0 {
		// A body we cannot parse is not fatal: the abort channel then reads false, which is
		// exactly how the ported runners already treat it (body ignored).
		_ = json.Unmarshal(data, &resp)
	}
	return resp.Abort, nil
}

// toSourceUpdate maps a RunStatus onto the lean SourceUpdateDTO wire shape. Message
// pointers are dereferenced to omitempty strings (null ≡ absent).
func toSourceUpdate(s RunStatus) meshapi.SourceUpdateDTO {
	var steps []meshapi.StepUpdateDTO
	if len(s.Steps) > 0 {
		steps = make([]meshapi.StepUpdateDTO, len(s.Steps))
		for i, step := range s.Steps {
			var outputs map[string]meshapi.OutputDTO
			if len(step.Outputs) > 0 {
				outputs = make(map[string]meshapi.OutputDTO, len(step.Outputs))
				for k, v := range step.Outputs {
					outputs[k] = meshapi.OutputDTO{Value: v.Value, Type: v.Type, Sensitive: v.Sensitive}
				}
			}
			steps[i] = meshapi.StepUpdateDTO{
				Id:            step.Name,
				DisplayName:   step.DisplayName,
				UserMessage:   deref(step.UserMessage),
				SystemMessage: deref(step.SystemMessage),
				Outputs:       outputs,
				Status:        step.Status.String(),
			}
		}
	}

	return meshapi.SourceUpdateDTO{
		Status: s.Status.String(),
		Steps:  steps,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
