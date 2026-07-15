// Package github is the GITHUB_WORKFLOW run handler: App
// JWT -> installation token -> workflow_dispatch, then async handover or sync run/job
// polling. It depends only on dispatch/meshapi/report/config + stdlib (depguard).
package github

import "time"

// Step ids and display names are FROZEN meshStack-wire strings: customer-visible in
// the run-status UI and pinned byte-identical from the Kotlin companion constants.
const (
	// StepId is the single trigger step registered before validation (Kotlin STEP_ID
	// "gh-trigger", GitHubBlockRunnerService.kt:593).
	StepId = "gh-trigger"
	// StepDisplayName is the trigger step's display name (":40").
	StepDisplayName = "Trigger GitHub Action"
	// jobStepIdPrefix + the job id forms one job's step id ("gh-workflow-job-<id>", :385).
	jobStepIdPrefix = "gh-workflow-job-"
	// jobDisplayNamePrefix + the job name forms a job step's display name (:386).
	jobDisplayNamePrefix = "GitHub Job: "
)

// Workflow-dispatch input key names — FROZEN toward customer workflows: the
// workflow_dispatch triggers declare exactly these input names.
const (
	inputKeyRunObject = "buildingBlockRun"
	inputKeyRunUrl    = "buildingBlockRunUrl"
	inputKeyApiToken  = "MESHSTACK_API_TOKEN"
	inputKeyRunToken  = "MESHSTACK_RUN_TOKEN"
	inputKeyEndpoint  = "MESHSTACK_ENDPOINT"
)

// GitHub API wire constants (frozen).
const (
	acceptHeader        = "application/vnd.github+json"
	apiVersionHeaderKey = "X-GitHub-Api-Version"
	apiVersionValue     = "2022-11-28"
)

// Polling/find constants — constructor defaults, FROZEN correlation window.
const (
	defaultFindAttempts = 12               // MAX_FIND_WORKFLOW_ATTEMPTS
	defaultPollInterval = 10 * time.Second // POLLING_INTERVAL_SECONDS
	defaultPollTimeout  = 30 * time.Minute // MAX_POLLING_MINUTES_WORKFLOWS
	findRunBufferWindow = 30 * time.Second // created_at > triggerTime - 30s
	listRunsPerPage     = 5                // the 5 newest runs of the workflow file
)

// behavior discriminators read off the run spec (frozen).
const (
	behaviorApply   = "APPLY"
	behaviorDetect  = "DETECT"
	behaviorDestroy = "DESTROY"
)
