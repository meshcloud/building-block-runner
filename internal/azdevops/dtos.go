// Package azdevops is the AZURE_DEVOPS_PIPELINE run handler:
// trigger an Azure DevOps pipeline run, then either hand over IN_PROGRESS (async) or poll to
// a terminal report with per-stage steps (sync). It is the heaviest of the four Kotlin
// ports -- Poller/StatusUpdater/StatusMapper had zero direct Kotlin tests, so this port's
// Go scenario suite (handler_scenario_test.go, poll_test.go, statusmapper_test.go,
// client_test.go) is the surviving pin for that behavior (see
// CROSS_REPO_TODO.md for why no NEW Kotlin JUnit tests were added in this environment).
//
// Built on the internal/manual template: the unified report.Reporter (stateless,
// abort discarded), config.SingleRunMode/BlockRunnerCompat/ResolvePrivateKey,
// dispatch.StandaloneClaimClassifier, and the per-type Dockerfile pattern. Package-local,
// not split into sibling packages: the Poller/StatusUpdater/StatusMapper Kotlin
// classes dissolve into pure functions + one Handler method, not real package seams.
package azdevops

// pipelineRun mirrors the Azure DevOps client/PipelineRun.kt DTO: package-local,
// not shared with any other type package (a sibling type package must not import
// another). Result is a pointer because Kotlin's PipelineRunResult
// is a genuinely nullable field distinct from its own "unknown" enum member (the
// null-result rendering vs the UNKNOWN wire value) -- the one place in this package a
// pointer is used for a simple field, precisely because zero ("") already means something
// else (the "" sentinel isn't available: pipelineRunResult's zero value would collide with
// a real, if unlikely, empty wire string).
type pipelineRun struct {
	Id           int64                        `json:"id"`
	Name         *string                      `json:"name,omitempty"`
	State        pipelineRunState             `json:"state"`
	Result       *pipelineRunResult           `json:"result,omitempty"`
	CreatedDate  string                       `json:"createdDate"`
	FinishedDate *string                      `json:"finishedDate,omitempty"`
	Url          *string                      `json:"url,omitempty"`
	Links        map[string]map[string]string `json:"_links,omitempty"`
}

// webURL reproduces `pipelineRun.links?.get("web")?.get("href") ?: pipelineRun.url ?: "N/A"`:
// fallback triggers only on an absent key/pointer, never on an empty string value.
func (pr pipelineRun) webURL() string {
	if pr.Links != nil {
		if web, ok := pr.Links["web"]; ok {
			if href, ok := web["href"]; ok {
				return href
			}
		}
	}
	if pr.Url != nil {
		return *pr.Url
	}
	return "N/A"
}

func isPipelineComplete(pr pipelineRun) bool {
	return pr.State == stateCompleted
}

// pipelineRunState mirrors PipelineRunState (client/PipelineRun.kt:27-34). The wire value is
// the @JsonValue string; enumName renders the Kotlin enum constant NAME instead (needed for
// final-message rendering, which uses Kotlin's default toString(), not .value).
// Unlike a Kotlin enum, an unrecognized wire string is representable here rather than
// failing to parse -- the tolerant-parse decision (an unknown state never blocks
// polling; enumName falls back to an uppercased echo of the raw value, never reached by any
// pinned fixture).
type pipelineRunState string

const (
	stateUnknownADO pipelineRunState = "unknown"
	stateInProgress pipelineRunState = "inProgress"
	stateCanceling  pipelineRunState = "canceling"
	stateCompleted  pipelineRunState = "completed"
)

func (s pipelineRunState) enumName() string {
	switch s {
	case stateUnknownADO:
		return "UNKNOWN"
	case stateInProgress:
		return "IN_PROGRESS"
	case stateCanceling:
		return "CANCELING"
	case stateCompleted:
		return "COMPLETED"
	default:
		return string(s)
	}
}

// pipelineRunResult mirrors PipelineRunResult (client/PipelineRun.kt:18-25).
type pipelineRunResult string

const (
	resultUnknownADO pipelineRunResult = "unknown"
	resultSucceeded  pipelineRunResult = "succeeded"
	resultFailed     pipelineRunResult = "failed"
	resultCanceled   pipelineRunResult = "canceled"
)

func (r pipelineRunResult) enumName() string {
	switch r {
	case resultUnknownADO:
		return "UNKNOWN"
	case resultSucceeded:
		return "SUCCEEDED"
	case resultFailed:
		return "FAILED"
	case resultCanceled:
		return "CANCELED"
	default:
		return string(r)
	}
}

// resultEnumNameOrNull renders the final-message result token: Kotlin's string-template
// toString() on a null PipelineRunResult? literally prints "null".
func resultEnumNameOrNull(r *pipelineRunResult) string {
	if r == nil {
		return "null"
	}
	return r.enumName()
}

// timelineResponse mirrors client/Timeline.kt's TimelineResponse.
type timelineResponse struct {
	Records []timelineRecord `json:"records"`
}

// timelineRecord mirrors client/Timeline.kt's TimelineRecord. State/Result/ParentId/Name are
// plain (non-pointer) strings: "" already means "absent" (Kotlin null) for every one of
// them, and none has a valid empty-string wire value to collide with.
type timelineRecord struct {
	Id         string               `json:"id"`
	Name       string               `json:"name,omitempty"`
	Type       string               `json:"type"`
	State      timelineRecordState  `json:"state,omitempty"`
	Result     timelineRecordResult `json:"result,omitempty"`
	StartTime  string               `json:"startTime,omitempty"`
	FinishTime string               `json:"finishTime,omitempty"`
	ParentId   string               `json:"parentId,omitempty"`
	Order      int                  `json:"order"`
}

// timelineTypeStage is the one TimelineRecordType value the stage filter cares about
// (client/Timeline.kt:24: TimelineRecordType.STAGE = "Stage").
const timelineTypeStage = "Stage"

// timelineRecordState mirrors TimelineRecordState (client/Timeline.kt:32-37; nullable, no
// UNKNOWN member in Kotlin -- an unrecognized wire string there fails the whole timeline
// parse). "" represents an absent/null state.
type timelineRecordState string

const (
	trsPending    timelineRecordState = "pending"
	trsInProgress timelineRecordState = "inProgress"
	trsCompleted  timelineRecordState = "completed"
)

// timelineRecordResult mirrors TimelineRecordResult (client/Timeline.kt:40-48). "" represents
// an absent/null result.
type timelineRecordResult string

const (
	trrSucceeded           timelineRecordResult = "succeeded"
	trrSucceededWithIssues timelineRecordResult = "succeededWithIssues"
	trrFailed              timelineRecordResult = "failed"
	trrCanceled            timelineRecordResult = "canceled"
	trrSkipped             timelineRecordResult = "skipped"
	trrAbandoned           timelineRecordResult = "abandoned"
)
