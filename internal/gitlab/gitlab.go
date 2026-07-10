// Package gitlab is the GITLAB_PIPELINE run handler: it triggers a GitLab CI/CD pipeline
// via one external POST (multipart form) and hands the run over asynchronously (D9) --
// the pipeline itself becomes the run's next status source via the callback URLs it
// receives. It performs no polling: terminal status arrives entirely pipeline-side
// (GitLabBlockRunnerService.kt; umbrella §3.2 "gitlab"). This is the second phase-6
// Kotlin port (06B): it reuses the 06A manual template verbatim (handler shape,
// event-driven reporting seam, config compat, persona wiring) and additionally ships the
// two umbrella-assigned shared artifacts this port needs first:
// meshapi.DecryptInputs (the secret-hygiene asymmetry, umbrella §7.6) and
// ExternalCallError (the MeshHttpException equivalent, 06A §4.4).
package gitlab

// Step id and display name are frozen, UI-visible strings (umbrella §7.1) -- the single
// step this runner registers and reports on (GitLabBlockRunnerService.kt:30-33,128-131).
const (
	StepId          = "gl-trigger"
	StepDisplayName = "Trigger GitLab CI/CD"
)

// User-facing message strings (umbrella §7.11 -- byte-identical to the Kotlin service,
// which reports the SAME pair for every trigger-path failure regardless of classification;
// classification (§2.3 rows A1-A4) is observable only in the systemMessage/logs, never as
// a distinct userMessage -- flag §16.1 of plan 06B).
const (
	userMessageTriggerFailed = "Could not trigger the GitLab pipeline"
	userMessageHandover      = "Triggered the configured GitLab pipeline"
)
