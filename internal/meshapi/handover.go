package meshapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// SanitizeRunObjectForHandover reduces spec.buildingBlockDefinition.spec.implementation to just
// its `type` field before the run JSON is handed over to an external system (e.g. gitlab's
// MESHSTACK_RUN, github's buildingBlockRun input). This is the structural fix for the leak
// hazard on the implementation side: a handover payload must never carry appPem /
// pipelineTriggerToken / personalAccessToken / sshPrivateKey, encrypted or not.
//
// This is a generic-JSON navigate-and-rewrite (decode with UseNumber, replace, re-encode)
// rather than a typed round-trip, so every other byte -- inputs, _links, runToken, behavior --
// passes through unchanged.
func SanitizeRunObjectForHandover(runJson []byte) ([]byte, error) {
	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(runJson))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing run JSON: %w", err)
	}

	implParent, implKey, impl, ok := navigateToImplementation(doc)
	if !ok {
		// No implementation object to sanitize (or malformed shape) -- pass the doc through
		// unchanged rather than failing a handover that doesn't need one.
		return json.Marshal(doc)
	}

	typ, _ := impl["type"].(string)
	implParent[implKey] = map[string]any{"type": typ}

	return json.Marshal(doc)
}

// navigateToImplementation walks spec.buildingBlockDefinition.spec.implementation, returning
// the parent map and key so the caller can replace the value in place, plus the decoded
// implementation object itself.
func navigateToImplementation(doc map[string]any) (parent map[string]any, key string, impl map[string]any, ok bool) {
	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		return nil, "", nil, false
	}
	def, ok := spec["buildingBlockDefinition"].(map[string]any)
	if !ok {
		return nil, "", nil, false
	}
	defSpec, ok := def["spec"].(map[string]any)
	if !ok {
		return nil, "", nil, false
	}
	impl, ok = defSpec["implementation"].(map[string]any)
	if !ok {
		return nil, "", nil, false
	}
	return defSpec, "implementation", impl, true
}

// SensitiveInputKeys returns the sorted keys of inputs marked isSensitive, so a caller can log
// a single WARN naming the sensitive inputs it forwards into a handover payload (where a pipeline
// may expose them unencrypted) rather than logging per-input.
func SensitiveInputKeys(inputs []BuildingBlockInputSpecDTO) []string {
	keys := make([]string, 0, len(inputs))
	for _, in := range inputs {
		if in.IsSensitive {
			keys = append(keys, in.Key)
		}
	}
	sort.Strings(keys)
	return keys
}
