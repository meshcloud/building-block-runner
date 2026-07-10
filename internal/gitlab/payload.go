package gitlab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime/multipart"
	"sort"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// runInputsProjection is the minimal projection of a (decrypted) run JSON needed to read
// inputs with number fidelity (json.Decoder.UseNumber, T6/§4.2) -- the gitlab-package twin
// of the manual persona's rawInputs (internal/manual/handler.go), duplicated rather than
// shared because a sibling persona package must not import another persona (D11).
type runInputsProjection struct {
	Spec struct {
		Behavior      string `json:"behavior"`
		BuildingBlock struct {
			Spec struct {
				Inputs []meshapi.BuildingBlockInputSpecDTO `json:"inputs"`
			} `json:"spec"`
		} `json:"buildingBlock"`
	} `json:"spec"`
}

// buildTriggerForm builds the frozen §2.2.2 multipart field set (the customer-pipeline
// contract, umbrella §8): token, ref, variables[MESHSTACK_BEHAVIOR], variables[MESHSTACK_RUN]
// (the DecryptInputs output verbatim -- raw-preserving, not a typed round-trip, §4.2/§16.6),
// variables[<key>]/inputs[<key>] per decrypted input (env vs non-env split, last-wins on
// duplicate keys), and the four callback URLs (missing link -> warn + omit, G-P2).
//
// decryptedRunJSON is meshapi.DecryptInputs's output: inputs decrypted, implementation
// secret and every other field untouched (the §7.6 asymmetry, made structural upstream --
// this function never sees ciphertext-vs-plaintext, it just serializes what it's given).
func buildTriggerForm(pipelineToken, refName string, decryptedRunJSON []byte, links meshapi.LinksDTO, log *slog.Logger) (*bytes.Buffer, string, error) {
	var proj runInputsProjection
	dec := json.NewDecoder(bytes.NewReader(decryptedRunJSON))
	dec.UseNumber()
	if err := dec.Decode(&proj); err != nil {
		return nil, "", fmt.Errorf("parsing decrypted run JSON for the trigger payload: %w", err)
	}

	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)

	writeField := func(name, value string) error { return w.WriteField(name, value) }

	if err := writeField("token", pipelineToken); err != nil {
		return nil, "", err
	}
	if err := writeField("ref", refName); err != nil {
		return nil, "", err
	}
	if err := writeField("variables[MESHSTACK_BEHAVIOR]", proj.Spec.Behavior); err != nil {
		return nil, "", err
	}
	if err := writeField("variables[MESHSTACK_RUN]", string(decryptedRunJSON)); err != nil {
		return nil, "", err
	}

	envVars, plainInputs := splitInputs(proj.Spec.BuildingBlock.Spec.Inputs)
	if err := writeSortedFields(writeField, "variables[%s]", envVars); err != nil {
		return nil, "", err
	}
	if err := writeSortedFields(writeField, "inputs[%s]", plainInputs); err != nil {
		return nil, "", err
	}

	for _, cb := range []struct {
		name string
		link meshapi.LinkDTO
	}{
		{"MESHSTACK_SELF_URL", links.Self},
		{"MESHSTACK_REGISTER_SOURCE_URL", links.RegisterSource},
		{"MESHSTACK_UPDATE_SOURCE_URL", links.UpdateSource},
		{"MESHSTACK_BASE_URL", links.MeshstackBaseUrl},
	} {
		if cb.link.Href == "" {
			log.Warn("could not extract callback URL from run links; omitting the corresponding variable",
				"variable", cb.name)
			continue
		}
		if err := writeField(fmt.Sprintf("variables[%s]", cb.name), cb.link.Href); err != nil {
			return nil, "", err
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return buf, w.FormDataContentType(), nil
}

// splitInputs partitions inputs by isEnvironment and dedups by key, last-wins (Kotlin's
// `associate`, GitLabClient.kt:126-142).
func splitInputs(inputs []meshapi.BuildingBlockInputSpecDTO) (env, plain map[string]any) {
	env = map[string]any{}
	plain = map[string]any{}
	for _, in := range inputs {
		if in.Env {
			env[in.Key] = in.Value
		} else {
			plain[in.Key] = in.Value
		}
	}
	return env, plain
}

// writeSortedFields writes one multipart field per map entry, in a stable (sorted-key)
// order for reproducible tests -- part order is not itself a contract (flag §16.7).
func writeSortedFields(writeField func(name, value string) error, nameFmt string, values map[string]any) error {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := writeField(fmt.Sprintf(nameFmt, k), valueString(values[k])); err != nil {
			return err
		}
	}
	return nil
}
