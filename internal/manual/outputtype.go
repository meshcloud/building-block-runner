// Package manual is the MANUAL run handler: it echoes a run's inputs back as outputs
// (1:1, with a fixed input→output type mapping) and reports a single terminal SUCCEEDED
// step. It performs no external calls and no decryption — in standalone mode sensitive
// inputs are echoed as ciphertext, in single-run mode the controller already decrypted
// them (NoOpBlockRunnerService.kt). It is the template port: the handler shape,
// event-driven reporting seam, config compat and type wiring the
// other three Kotlin ports (gitlab/azdevops/github) reuse.
package manual

// Step ids/display names are frozen, UI-visible strings. The production
// service registers and reports exactly the "manual" step; debug mode adds a second
// throwaway step.
const (
	StepId          = "manual"
	StepDisplayName = "Manual Block Run"

	debugStepId        = "additionalDebugStep"
	debugUserMessage   = "this is a message for the user"
	debugSystemMessage = "this is a message for the system"
)

// meshStack input/output type strings (MeshBuildingBlockIOType). Duplicated here as
// package-local constants rather than reaching into internal/tf's DataType (a sibling
// type package must not import another type), matching the enum in
// MeshBuildingBlockIO.kt:9-18.
const (
	typeString       = "STRING"
	typeInteger      = "INTEGER"
	typeBoolean      = "BOOLEAN"
	typeCode         = "CODE"
	typeFile         = "FILE"
	typeList         = "LIST"
	typeSingleSelect = "SINGLE_SELECT"
	typeMultiSelect  = "MULTI_SELECT"
)

// toOutputType maps an input type to the output type the manual runner echoes, exactly as
// NoOpBlockRunnerService.toOutputType (:77-88): identity for STRING/INTEGER/BOOLEAN/CODE,
// FILE→STRING, LIST→CODE, SINGLE_SELECT→STRING, MULTI_SELECT→CODE. The bool return reports
// whether the input type was a known enum member; an unknown value maps to itself
// (identity passthrough) so the handler can warn rather than fail the run. Kotlin could
// never reach the unknown case (Jackson enum parsing would already have failed the whole
// claim), so inventing a run-failing path here would be new behavior.
func toOutputType(inputType string) (string, bool) {
	switch inputType {
	case typeString, typeInteger, typeBoolean, typeCode:
		return inputType, true
	case typeFile:
		return typeString, true
	case typeList:
		return typeCode, true
	case typeSingleSelect:
		return typeString, true
	case typeMultiSelect:
		return typeCode, true
	default:
		return inputType, false
	}
}
