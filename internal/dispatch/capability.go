// Package dispatch holds the backend-agnostic pieces of run dispatch: the capability a
// runner is configured with, and the claim-and-fail-fast signal a Dispatcher raises when a
// claimed run's type has neither an in-process handler nor a k8s job template.
package dispatch

import (
	"fmt"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// Capability is a runner's registered capability: one concrete RunnerImplementationType, or
// ALL. The backend's BuildingBlockRunnerCapabilityType is a single-valued enum
// (meshapi.RunnerImplementationType) -- subsets are not representable, so this type does not
// pretend a runner can register for e.g. "TERRAFORM,MANUAL". Capability feeds registration
// only: what a runner actually claims is decided server-side by the
// registered type of its runner object, and claim-and-fail-fast (UnhandledTypeError below)
// is unconditional regardless of the configured capability.
type Capability meshapi.RunnerImplementationType

// String returns the wire value ("TERRAFORM", "ALL", ...) -- what registration DTOs and log
// lines show.
func (c Capability) String() string {
	return string(c)
}

// validCapabilities is the fixed, backend-defined set (5 concrete types + ALL); a runner
// cannot invent a new one, so this is a closed switch, not an open-ended lookup table.
var validCapabilities = map[meshapi.RunnerImplementationType]bool{
	meshapi.RunnerTypeManual:              true,
	meshapi.RunnerTypeTerraform:           true,
	meshapi.RunnerTypeGitHubWorkflow:      true,
	meshapi.RunnerTypeGitLabPipeline:      true,
	meshapi.RunnerTypeAzureDevOpsPipeline: true,
	meshapi.RunnerTypeAll:                 true,
}

// ParseCapability validates s against the 5 concrete RunnerImplementationType values plus
// ALL. Config validation must fail fast on a typo'd or stale capability value at
// startup, rather than let the runner register with a value the backend silently rejects or
// -- worse -- claim runs it never intended to serve.
func ParseCapability(s string) (Capability, error) {
	t := meshapi.RunnerImplementationType(s)
	if !validCapabilities[t] {
		return "", fmt.Errorf(
			"invalid capability %q: must be one of MANUAL, TERRAFORM, GITHUB_WORKFLOW, GITLAB_PIPELINE, AZURE_DEVOPS_PIPELINE, ALL", s)
	}
	return Capability(t), nil
}
