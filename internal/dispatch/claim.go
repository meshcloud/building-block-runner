package dispatch

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// RunClaimClient is the loop's own meshapi adapter: it claims runs and reports fail-fast
// failures. It is the dissolution target of the former internal/controller/runapi.go
// -- moved, not changed: node-ids, media types and the base64 claim
// handover are byte-identical.
type RunClaimClient struct {
	url             string
	runnerUuid      string
	requesterPrefix string
	auth            meshapi.AuthProvider
	identity        meshapi.Identity
	metrics         *MetricsCollector
	// requesterFn builds the node-id from the runner uuid. Defaults to the prefix-first
	// "<requesterPrefix>-<runnerUuid>" shape; a caller with a differently-shaped frozen
	// header (the tf type's "<runnerUuid>-worker-1") overrides it via WithRequester.
	requesterFn func(runnerUuid string) string
}

// ClaimOption customizes a RunClaimClient at construction (functional options keep the
// positional constructor signature stable for the controller and runner types).
type ClaimOption func(*RunClaimClient)

// WithRequester overrides the node-id builder. The default is
// "<requesterPrefix>-<runnerUuid>" (controller "run-controller-<uuid>"; runner types
// "<identity.Name>-<uuid>"). The tf type needs the frozen "<runnerUuid>-worker-1" node-id
// (an observable header), which is uuid-first and so does not fit the prefix-first default.
func WithRequester(fn func(runnerUuid string) string) ClaimOption {
	return func(c *RunClaimClient) { c.requesterFn = fn }
}

// NewRunClaimClient builds the claim/fail-fast-report adapter for one runner/controller
// identity. By default requesterPrefix is stamped into the node-id as "<prefix>-<runnerUuid>"
// (e.g. "run-controller-<uuid>", the frozen controller header) -- callers own their own
// frozen prefix; pass WithRequester to stamp a differently-shaped frozen node-id.
func NewRunClaimClient(url, runnerUuid, requesterPrefix string, auth meshapi.AuthProvider, identity meshapi.Identity, metrics *MetricsCollector, opts ...ClaimOption) *RunClaimClient {
	c := &RunClaimClient{
		url:             url,
		runnerUuid:      runnerUuid,
		requesterPrefix: requesterPrefix,
		auth:            auth,
		identity:        identity,
		metrics:         metrics,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *RunClaimClient) newMeshClient(nodeID string) *meshapi.RunClient {
	return meshapi.NewRunClient(c.url, nodeID, c.auth, meshapi.WithIdentity(c.identity))
}

func (c *RunClaimClient) requester() string {
	if c.requesterFn != nil {
		return c.requesterFn(c.runnerUuid)
	}
	return fmt.Sprintf("%s-%s", c.requesterPrefix, c.runnerUuid)
}

// Claim fetches the next available run for this identity. The returned ClaimedRun.Type is
// the zero value -- Loop resolves it from Details before dispatching, since the
// implementation-type discriminator is never itself encrypted (no decrypt needed here).
func (c *RunClaimClient) Claim() (ClaimedRun, error) {
	start := time.Now()
	defer func() {
		c.metrics.ObserveRunsFetchDuration(c.runnerUuid, time.Since(start).Seconds())
	}()

	client := c.newMeshClient(c.requester())
	dto, rawBytes, err := client.FetchRun(c.runnerUuid)
	if err != nil {
		// Count every fetch failure except the frozen "no run available" 404 signal, which
		// is the normal idle-poll outcome, not an API error.
		if he, ok := meshapi.AsHttpError(err); !ok || !he.IsNotFound() {
			c.metrics.IncRunsFetchError(c.runnerUuid, ErrorTypeFetchAPI)
		}
		return ClaimedRun{}, err
	}

	return ClaimedRun{
		Id:      RunId(dto.Metadata.Uuid),
		Details: dto,
		RawJson: base64.StdEncoding.EncodeToString(rawBytes),
	}, nil
}

// RegisterSource registers this identity as a status source for a run. Idempotent: if the
// source is already registered (HTTP 409 Conflict), the call succeeds silently (meshapi
// house behavior).
func (c *RunClaimClient) RegisterSource(runId RunId) error {
	client := c.newMeshClient(c.requester())

	registration := meshapi.RegistrationDTO{
		Source: meshapi.SourceDTO{
			Id: c.runnerUuid,
		},
		Steps: []meshapi.StepRegistrationDTO{
			{
				Id:          "validation",
				DisplayName: "Validation",
			},
		},
	}

	if err := client.RegisterSource(string(runId), registration); err != nil {
		return fmt.Errorf("register source failed: %w", err)
	}
	return nil
}

// UpdateRunStatus sends a PATCH status update for a run. Must be called after
// RegisterSource has been called for the same run.
func (c *RunClaimClient) UpdateRunStatus(runId RunId, status, summary, stepMessage string) error {
	client := c.newMeshClient(c.requester())

	dto := meshapi.StatusUpdateDTO{
		Status:  &status,
		Summary: &summary,
		Steps: []meshapi.StepStatusUpdateDTO{
			{
				Id:            "validation",
				DisplayName:   "Validation",
				Status:        &status,
				UserMessage:   &stepMessage,
				SystemMessage: &stepMessage,
			},
		},
	}

	if _, err := client.PatchStatus(string(runId), c.runnerUuid, dto); err != nil {
		return fmt.Errorf("update status failed: %w", err)
	}
	return nil
}

// isNoRunError reports whether err is the "no runs available" (HTTP 404) signal.
func isNoRunError(err error) bool {
	if he, ok := meshapi.AsHttpError(err); ok {
		return he.IsNotFound()
	}
	return false
}

// ControllerClaimClassifier is the run-controller type's claim-error policy (frozen):
// a 404 is the normal idle-poll outcome (not logged); any other
// error is logged but still just waits for the next tick -- the controller has no backoff
// concept distinct from its regular polling interval.
func ControllerClaimClassifier(err error) ClaimOutcome {
	if isNoRunError(err) {
		return OutcomeNoRun
	}
	return OutcomeNoRunLogged
}
