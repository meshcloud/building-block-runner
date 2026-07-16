package meshapi

// SourceUpdateDTO is the lean status-PATCH body the four Kotlin-port runners
// (manual/gitlab/azdevops/github) send — the THIRD wire shape on the runner-facing
// status-source endpoint, distinct from and leaner than tf's RunStatusUpdateDTO and the
// controller's StatusUpdateDTO. It mirrors the Kotlin
// MeshBuildingBlockRun.SourceUpdate (block-runner-core) byte-for-byte at the parsed-JSON
// level: only `status` + `steps`, every field omitempty so a status-only or steps-only
// update carries just what changed.
//
// The Kotlin Jackson mapper serializes unset optional fields as explicit JSON null;
// these DTOs use omitempty instead, so wire comparisons are made at parsed-JSON level
// with null treated as absent. The meshfed endpoint upserts steps by id,
// so sending only the changed/new steps is safe, and each included step carries its FULL
// current message text (the backend overwrites by assignment, never appends).
type SourceUpdateDTO struct {
	Status string          `json:"status,omitempty"`
	Steps  []StepUpdateDTO `json:"steps,omitempty"`
}

// StepUpdateDTO is one step inside a SourceUpdateDTO. Unlike tf's StepStatusUpdateDTO
// (pointer-and-omitempty message fields), the message/status fields here are plain
// omitempty strings: absent ("") ≡ the Kotlin null, so the reporting seam need not juggle
// *string just to reproduce "field not sent" (the simple-nullable rule — "" already means
// "not present" for these UI message fields).
type StepUpdateDTO struct {
	Id            string               `json:"id"`
	DisplayName   string               `json:"displayName,omitempty"`
	UserMessage   string               `json:"userMessage,omitempty"`
	SystemMessage string               `json:"systemMessage,omitempty"`
	Outputs       map[string]OutputDTO `json:"outputs,omitempty"`
	Status        string               `json:"status,omitempty"`
}
