package meshapi

import (
	"fmt"
	"log/slog"
)

// Input types eligible for the input-only decryption DecryptInputSpecs performs -- the
// exact three Kotlin MeshCertDecryptionService.decryptBlockRunInputs branches
// (block-runner-core/.../MeshCertDecryptionService.kt:58-97). Duplicated here as literal
// strings rather than importing a runner-specific DataType enum (no such shared enum
// exists at this layer; the meshapi package stays DTO-only, P3).
const (
	ioTypeString = "STRING"
	ioTypeCode   = "CODE"
	ioTypeFile   = "FILE"
)

// DecryptInputSpecs returns a value-copy of inputs (P4) with every sensitive STRING/CODE/
// FILE input decrypted via dec; every other sensitive type (BOOLEAN/INTEGER/LIST/
// SINGLE_SELECT/MULTI_SELECT) is left as ciphertext and logged -- the Kotlin
// decryptBlockRunInputs asymmetry preserved verbatim (umbrella §4 row 8, §7.6): this
// function NEVER touches an implementation's own secrets (e.g. a PAT/trigger token), only
// the building-block's declared inputs, so the impl-secret-vs-input asymmetry the Kotlin
// ports rely on (secret hygiene: an implementation secret must never round-trip through a
// decrypted-inputs payload) is structural, not a call-site discipline.
//
// This is the typed-DTO twin of DecryptInputs (the raw-JSON, byte-preserving variant the
// gitlab payload path needs, §16.6): azure-devops already holds the parsed input specs and
// builds template parameters from them, so it decrypts the typed slice directly rather than
// round-tripping the whole run document. Both live in meshapi because both are the shared
// Kotlin decryptBlockRunInputs contract (umbrella §4 row 8) viewed from the two shapes its
// consumers legitimately need; the branch rules (STRING/CODE/FILE only) are identical.
//
// A non-sensitive input, and a sensitive input whose Value is not a string, pass through
// unchanged: Kotlin's `input.value.toString()` never fails (Any.toString() is total), so a
// decrypt attempt over a genuinely non-string sensitive value would be new, invented
// failure surface with no Kotlin twin -- fmt.Sprint reproduces that total stringification
// here.
func DecryptInputSpecs(dec Decryptor, log *slog.Logger, inputs []BuildingBlockInputSpecDTO) ([]BuildingBlockInputSpecDTO, error) {
	if log == nil {
		log = slog.Default()
	}
	if len(inputs) == 0 {
		return inputs, nil
	}

	out := make([]BuildingBlockInputSpecDTO, len(inputs))
	for i, in := range inputs {
		out[i] = in
		if !in.IsSensitive {
			continue
		}

		switch in.Type {
		case ioTypeString, ioTypeCode, ioTypeFile:
			ciphertext := fmt.Sprint(in.Value)
			plaintext, err := dec.Decrypt(ciphertext)
			if err != nil {
				return nil, fmt.Errorf("decrypting sensitive input %q: %w", in.Key, err)
			}
			out[i].Value = plaintext
		default:
			log.Error("cannot decrypt a sensitive input that is neither STRING, CODE, or FILE; leaving as-is",
				"key", in.Key, "type", in.Type)
		}
	}
	return out, nil
}
