package manual

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

// debugWaitDelay is the dev-only pause between debug-mode updates (Kotlin
// DebugBlockRunnerService Thread.sleep(5000)). The exact cadence is not a contract
// (umbrella §3.2) — it is injectable via Clock so tests run instantly.
const debugWaitDelay = 5 * time.Second

// Clock abstracts the debug-mode inter-update wait so tests are deterministic and instant.
// Wait blocks for d or until ctx is cancelled, whichever comes first.
type Clock interface {
	Wait(ctx context.Context, d time.Duration)
}

// RealClock waits on a real timer, honoring context cancellation.
type RealClock struct{}

func (RealClock) Wait(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// ReporterFactory builds a run-scoped report.Reporter for one claimed run (runToken-only
// auth underneath — the handler never touches the runner's claim credentials). It is the
// injected seam (P3): cmd/manual wires the real meshapi-backed factory, tests wire one
// pointing at the meshapitest server.
type ReporterFactory func(run dispatch.ClaimedRun) report.Reporter

// HandlerDeps are the manual handler's injected collaborators. Deliberately NO
// meshapi.Decryptor: the manual runner never decrypts (§2.1.6) — the umbrella §5.3
// template names one for every handler, and 06B–D add it, so this omission is a
// manual-specific narrowing, not a template change (§16.1/§17).
type HandlerDeps struct {
	Reporters ReporterFactory
	Clock     Clock
	Rand      func() float64 // debug-mode outcome; injectable (Kotlin Math.random)
	Log       *slog.Logger
}

// Handler is the MANUAL run handler (value type, P4). It satisfies dispatch.RunHandler.
type Handler struct {
	cfg  Config
	deps HandlerDeps
}

// NewHandler builds the manual handler. A nil Clock/Rand/Log fall back to sensible
// defaults so a minimally-wired handler is always usable (P8).
func NewHandler(cfg Config, deps HandlerDeps) Handler {
	if deps.Clock == nil {
		deps.Clock = RealClock{}
	}
	if deps.Rand == nil {
		deps.Rand = defaultRand
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return Handler{cfg: cfg, deps: deps}
}

// Execute runs one MANUAL run to completion: register the single "manual" step, then echo
// the inputs as outputs in one terminal SUCCEEDED update (production) or run the debug
// update sequence (debugMode). It follows the RunHandler contract (dispatch A1): a
// register/report transport failure returns a non-nil error (infrastructure failure — the
// run stays unreported exactly as in Kotlin, §2.5); manual never reports run-level FAILED
// because no execution can fail. The reporter's abort return is discarded (umbrella §7.5).
func (h Handler) Execute(ctx context.Context, run dispatch.ClaimedRun) error {
	log := h.deps.Log.With("run", run.Id)
	reporter := h.deps.Reporters(run)

	// Register exactly the "manual" step (Kotlin registerAsSource in processBlock, C-P3).
	register := report.RunStatus{
		RunId: string(run.Id),
		Steps: []report.StepStatus{{Name: StepId, DisplayName: StepDisplayName}},
	}
	if err := reporter.Register(register); err != nil {
		return err
	}

	inputs, err := h.decodeInputs(run, log)
	if err != nil {
		return err
	}

	if h.cfg.DebugMode {
		return h.executeDebug(ctx, reporter, string(run.Id), inputs, log)
	}
	return h.executeEcho(reporter, string(run.Id), inputs, log)
}

// executeEcho sends the one production update: run SUCCEEDED, step "manual" SUCCEEDED,
// outputs = inputs echoed 1:1 (last-wins on duplicate keys, type via toOutputType,
// sensitivity flag preserved).
func (h Handler) executeEcho(reporter report.Reporter, runId string, inputs []meshapi.BuildingBlockInputSpecDTO, log *slog.Logger) error {
	status := report.RunStatus{
		RunId:  runId,
		Status: report.SUCCEEDED,
		Steps: []report.StepStatus{{
			Name:        StepId,
			DisplayName: StepDisplayName,
			Status:      report.SUCCEEDED,
			Outputs:     echoOutputs(inputs, toOutputType, log),
		}},
	}
	_, err := reporter.Report(status)
	return err
}

// echoOutputs builds the outputs map from inputs: value verbatim, type via typeOf,
// sensitivity preserved, last-wins on duplicate keys (Kotlin associateBy, M-P2).
func echoOutputs(inputs []meshapi.BuildingBlockInputSpecDTO, typeOf func(string) (string, bool), log *slog.Logger) map[string]report.Output {
	outputs := make(map[string]report.Output, len(inputs))
	for _, in := range inputs {
		outType, known := typeOf(in.Type)
		if !known {
			log.Warn("unknown input type; echoing output type unchanged", "key", in.Key, "type", in.Type)
		}
		outputs[in.Key] = report.Output{Value: in.Value, Type: outType, Sensitive: in.IsSensitive}
	}
	return outputs
}

// decodeInputs reads the run's inputs with number fidelity: default encoding/json
// float64-izes INTEGER values and can reformat large numbers, so the raw claimed/file
// bytes are re-decoded with json.Decoder.UseNumber() (values become json.Number, echoed
// back byte-faithfully — M-P3). This is a handler-visible template requirement recorded
// for gitlab/github, which embed run JSON in outbound payloads (§4.2/§17). When RawJson is
// empty (defensive), it falls back to the already-parsed Details.
func (h Handler) decodeInputs(run dispatch.ClaimedRun, log *slog.Logger) ([]meshapi.BuildingBlockInputSpecDTO, error) {
	if run.RawJson != "" {
		raw, err := base64.StdEncoding.DecodeString(run.RawJson)
		if err != nil {
			log.Warn("run raw JSON is not valid base64; using parsed details for inputs", "err", err)
		} else {
			var parsed rawInputs
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			if err := dec.Decode(&parsed); err != nil {
				return nil, err
			}
			return parsed.Spec.BuildingBlock.Spec.Inputs, nil
		}
	}
	if run.Details != nil {
		return run.Details.Spec.BuildingBlock.Spec.Inputs, nil
	}
	return nil, nil
}

// rawInputs is the minimal projection of a run JSON needed to read inputs with UseNumber.
type rawInputs struct {
	Spec struct {
		BuildingBlock struct {
			Spec struct {
				Inputs []meshapi.BuildingBlockInputSpecDTO `json:"inputs"`
			} `json:"spec"`
		} `json:"buildingBlock"`
	} `json:"spec"`
}

func defaultRand() float64 { return 0 }
