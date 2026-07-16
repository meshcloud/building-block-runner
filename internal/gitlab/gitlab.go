// Package gitlab is the GITLAB_PIPELINE run handler: it triggers a GitLab CI/CD pipeline
// via one external POST (multipart form) and hands the run over asynchronously --
// the pipeline itself becomes the run's next status source via the callback URLs it
// receives. It performs no polling: terminal status arrives entirely pipeline-side
// (GitLabBlockRunnerService.kt). This is the second
// Kotlin port: it reuses the manual template verbatim (handler shape,
// event-driven reporting seam, config compat, type wiring) and additionally ships the
// two shared artifacts this port needs first:
// meshapi.SanitizeRunObjectForHandover (impl-stripping the outbound run object) and
// ExternalCallError (the MeshHttpException equivalent).
package gitlab

// Step id and display name are frozen, UI-visible strings -- the single
// step this runner registers and reports on (GitLabBlockRunnerService.kt:30-33,128-131).
const (
	StepId          = "gl-trigger"
	StepDisplayName = "Trigger GitLab CI/CD"
)

// User-facing message strings (byte-identical to the Kotlin service,
// which reports the SAME pair for every trigger-path failure regardless of classification;
// classification (rows A1-A4) is observable only in the systemMessage/logs, never as
// a distinct userMessage).
const (
	userMessageTriggerFailed = "Could not trigger the GitLab pipeline"
	userMessageHandover      = "Triggered the configured GitLab pipeline"
	// userMessageAborted has no Kotlin counterpart: it is new for the backend-requested
	// runAborted abort (T1).
	userMessageAborted = "The GitLab pipeline run was aborted"
)
