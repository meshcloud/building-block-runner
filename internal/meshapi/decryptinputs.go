package meshapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
)

// decryptableInputTypes are the input types the payload path decrypts (umbrella §4 row 8,
// §7.6): MeshCertDecryptionService.decryptBlockRunInputs (:58-97) restricts to
// STRING/CODE/FILE, warn-and-skip for any other sensitive type. This is deliberately
// NARROWER than the k8s controller's own DecryptRunDetails input rule (any sensitive
// string, regardless of type) -- the two rules coexist in the binary on purpose (flag
// §16.8 of plan 06B): DecryptRunDetails serves the k8s Secret handover, DecryptInputs
// serves outbound payloads built from Kotlin-parity rules.
var decryptableInputTypes = map[string]bool{
	"STRING": true,
	"CODE":   true,
	"FILE":   true,
}

// DecryptInputs decrypts ONLY the sensitive input values of a claimed run JSON (STRING/
// CODE/FILE types; other sensitive types are left as-is with a warning) -- it never
// touches the implementation object, _links, runToken, or any other field. This is the
// structural fix for the umbrella §10.9 leak hazard: an outbound payload to an external
// system (gitlab's MESHSTACK_RUN, github's buildingBlockRun, ...) MUST be built from this
// function's output, never from a decryption pass that also decrypts implementation
// secrets (that would leak e.g. a plaintext pipeline-trigger-token into the payload it
// itself carries encrypted, umbrella §7.6).
//
// rawRunJson is the claimed run JSON bytes (not base64, not yet decrypted). The result is
// a generic-JSON transform (decode with UseNumber, replace matching input values, re-
// encode) rather than a typed round-trip: every other byte's semantics -- including
// fields this DTO doesn't model -- passes through unchanged (a deliberately MORE faithful
// forwarding than the Kotlin typed round-trip, plan 06B §16.6). With a NoopDecryptor
// (single-run mode) this is an identity transform modulo re-marshaling.
func DecryptInputs(rawRunJson []byte, dec Decryptor, log *slog.Logger) ([]byte, error) {
	if log == nil {
		log = slog.Default()
	}

	var doc map[string]any
	d := json.NewDecoder(bytes.NewReader(rawRunJson))
	d.UseNumber()
	if err := d.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parsing run JSON: %w", err)
	}

	inputs, err := navigateToInputs(doc)
	if err != nil {
		return nil, err
	}

	for _, raw := range inputs {
		input, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		sensitive, _ := input["isSensitive"].(bool)
		if !sensitive {
			continue
		}
		typ, _ := input["type"].(string)
		if !decryptableInputTypes[typ] {
			log.Warn("cannot decrypt a sensitive input that is neither STRING, CODE, or FILE; leaving as is",
				"key", input["key"], "type", typ)
			continue
		}
		strVal, ok := input["value"].(string)
		if !ok {
			// A sensitive STRING/CODE/FILE input whose value isn't itself a JSON string is not
			// representable in the Kotlin model either; leave it untouched defensively (P5 favors
			// failing loudly over corrupting data, but this shape cannot occur from a well-formed
			// claim, so silent passthrough here mirrors "value.toString()" safety, not a new rule).
			continue
		}
		decrypted, err := dec.Decrypt(strVal)
		if err != nil {
			return nil, fmt.Errorf("decrypting input %q: %w", input["key"], err)
		}
		input["value"] = decrypted
	}

	return json.Marshal(doc)
}

// navigateToInputs walks spec.buildingBlock.spec.inputs -- the same path
// BuildingBlockInputSpecDTO models -- returning the raw (map-decoded) input list so
// DecryptInputs can rewrite values in place without a typed round-trip.
func navigateToInputs(doc map[string]any) ([]any, error) {
	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("run JSON has no spec object")
	}
	bb, ok := spec["buildingBlock"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("run JSON has no spec.buildingBlock object")
	}
	bbSpec, ok := bb["spec"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("run JSON has no spec.buildingBlock.spec object")
	}
	inputs, ok := bbSpec["inputs"].([]any)
	if !ok {
		// No inputs array (or wrong type) - not necessarily malformed, e.g. a building block with
		// zero inputs may omit or null the field; nothing to decrypt.
		return nil, nil
	}
	return inputs, nil
}
