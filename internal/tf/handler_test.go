package tf

import (
	"errors"
	"testing"
	"time"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Test_NewClaimClassifier reproduces the former Worker.handleFetchRunError taxonomy as a
// dispatch.ClaimClassifier: 404/409 => no-run (409 logged); the chunked-transfer-encoding
// transport glitch => no-run; anything else => backoff + a poll-error meter tick.
func Test_NewClaimClassifier(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		want        dispatch.ClaimOutcome
		wantPollErr int
	}{
		{"not found", meshapi.HttpError{StatusCode: 404}, dispatch.OutcomeNoRun, 0},
		{"conflict", meshapi.HttpError{StatusCode: 409}, dispatch.OutcomeNoRunLogged, 0},
		{"server error", meshapi.HttpError{StatusCode: 500}, dispatch.OutcomeBackoff, 1},
		{
			"chunked transport glitch",
			errors.New("net/http: HTTP/1.x transport connection broken: too many transfer encodings: [\"chunked\" \"chunked\"]"),
			dispatch.OutcomeNoRun,
			0,
		},
		{"other transport error", errors.New("connection refused"), dispatch.OutcomeBackoff, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meter := &fakeMeter{}
			classify := NewClaimClassifier(meter)
			if got := classify(tt.err); got != tt.want {
				t.Errorf("classify(%v) = %v, want %v", tt.err, got, tt.want)
			}
			if got := meter.snapshot().pollErrors; got != tt.wantPollErr {
				t.Errorf("pollErrors = %d, want %d", got, tt.wantPollErr)
			}
		})
	}
}

func Test_NewClaimClassifier_NilMeterDoesNotPanic(t *testing.T) {
	classify := NewClaimClassifier(nil)
	if got := classify(meshapi.HttpError{StatusCode: 500}); got != dispatch.OutcomeBackoff {
		t.Errorf("expected OutcomeBackoff, got %v", got)
	}
}

func Test_NewHandler_FillsDefaults(t *testing.T) {
	// A minimally-wired handler (nil Meter/Log/NewRunApi) must be usable: every optional
	// dep falls back to a working default.
	h := NewHandler(HandlerConfig{WorkingDir: "/tmp", TfCommandTimeout: 5 * time.Minute}, HandlerDeps{})
	if h.meter == nil {
		t.Error("expected a non-nil meter default")
	}
	if h.log == nil {
		t.Error("expected a non-nil logger default")
	}
	if h.newRunApi == nil {
		t.Error("expected a non-nil RunApi factory default")
	}
	if h.timeout != 5*time.Minute {
		t.Errorf("expected 5m timeout, got %v", h.timeout)
	}
}

func Test_DefaultRunApiFactory_SetsRunToken(t *testing.T) {
	// With no NewRunApi injected, NewHandler builds the production run-scoped factory from the
	// threaded RunnerUuid/ApiBackend (replacing the former AppConfig global).
	h := NewHandler(HandlerConfig{
		RunnerUuid: "u",
		ApiBackend: RunApiConfig{Url: "http://localhost"},
	}, HandlerDeps{})
	api := h.newRunApi("run-token-abc")
	client, ok := api.(*RunApiClient)
	if !ok {
		t.Fatalf("expected *RunApiClient, got %T", api)
	}
	if client.auth.runToken == nil || *client.auth.runToken != "run-token-abc" {
		t.Errorf("expected runToken %q wired into the client, got %v", "run-token-abc", client.auth.runToken)
	}
	if client.rid != "u" {
		t.Errorf("expected rid %q threaded into the client, got %q", "u", client.rid)
	}
}
