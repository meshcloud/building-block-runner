package azdevops

// Frozen, UI-visible step id/display name: the coordinator's status
// UI keys off these strings, so they are typed constants, never re-derived at call sites.
const (
	StepId             = "azure-devops-trigger"
	triggerDisplayName = "Trigger Azure DevOps Pipeline"
)

// stageStepId is the frozen "ado-stage-<id>" step id.
func stageStepId(timelineID string) string {
	return "ado-stage-" + timelineID
}

// stageDisplayName is the frozen "Stage: <name>" display name.
func stageDisplayName(name string) string {
	return "Stage: " + name
}
