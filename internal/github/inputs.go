package github

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/meshcloud/building-block-runner/internal/meshapi"
)

// dispatchPayload is the workflow_dispatch request body: {ref, inputs} (§2.4). ref is the
// implementation branch (frozen §13).
type dispatchPayload struct {
	Ref    string            `json:"ref"`
	Inputs map[string]string `json:"inputs"`
}

// runInput is one decrypted run input carrying value fidelity: non-sensitive numeric values
// stay json.Number (D8), sensitive STRING/CODE/FILE values become decrypted strings.
type runInput struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Type        string `json:"type"`
	IsSensitive bool   `json:"isSensitive"`
	Env         bool   `json:"isEnvironment"`
}

// decryptedRun bundles what both input modes read: the parsed run details (scalars +
// links) and the decrypted inputs (with json.Number fidelity). It is built once per run.
type decryptedRun struct {
	details *meshapi.RunDetailsDTO
	inputs  []runInput
}

// selfHref returns the run's self-link href, or "" when absent.
func (r decryptedRun) selfHref() string {
	if r.details == nil {
		return ""
	}
	return r.details.Links.Self.Href
}

// decodeAndDecryptInputs reads the run inputs from the raw claimed JSON with
// json.Decoder.UseNumber() (D8 — numbers survive as json.Number so "value": 4 round-trips
// as a number, not float64), then decrypts sensitive ones per §2.4/umbrella §4 row 8:
// sensitive STRING/CODE/FILE are decrypted; a sensitive input of any other type is logged
// and left as-is; non-sensitive inputs are untouched. rawJSONBase64 is run.RawJson;
// falling back to the already-parsed Details when it is empty/undecodable (defensive).
func decodeAndDecryptInputs(rawJSONBase64 string, details *meshapi.RunDetailsDTO, dec Decryptor, log *slog.Logger) ([]runInput, error) {
	raw := readRawInputs(rawJSONBase64, details, log)
	out := make([]runInput, 0, len(raw))
	for _, in := range raw {
		if in.IsSensitive {
			switch in.Type {
			case "STRING", "CODE", "FILE":
				plain, err := dec.Decrypt(valueToString(in.Value))
				if err != nil {
					return nil, fmt.Errorf("decrypting sensitive input %q: %w", in.Key, err)
				}
				in.Value = plain
			default:
				log.Error("cannot decrypt a sensitive input that is neither STRING, CODE, nor FILE; leaving it as-is",
					"key", in.Key, "type", in.Type)
			}
		}
		out = append(out, in)
	}
	return out, nil
}

// readRawInputs decodes just the inputs slice with UseNumber; on any failure it falls back
// to Details (values there are already generic-typed).
func readRawInputs(rawJSONBase64 string, details *meshapi.RunDetailsDTO, log *slog.Logger) []runInput {
	if rawJSONBase64 != "" {
		if data, err := base64.StdEncoding.DecodeString(rawJSONBase64); err != nil {
			log.Warn("run raw JSON is not valid base64; using parsed details for inputs", "err", err)
		} else {
			var parsed struct {
				Spec struct {
					BuildingBlock struct {
						Spec struct {
							Inputs []runInput `json:"inputs"`
						} `json:"spec"`
					} `json:"buildingBlock"`
				} `json:"spec"`
			}
			d := json.NewDecoder(bytes.NewReader(data))
			d.UseNumber()
			if err := d.Decode(&parsed); err != nil {
				log.Warn("run raw JSON not decodable; using parsed details for inputs", "err", err)
			} else {
				return parsed.Spec.BuildingBlock.Spec.Inputs
			}
		}
	}
	if details != nil {
		src := details.Spec.BuildingBlock.Spec.Inputs
		out := make([]runInput, len(src))
		for i, in := range src {
			out[i] = runInput{Key: in.Key, Value: in.Value, Type: in.Type, IsSensitive: in.IsSensitive, Env: in.Env}
		}
		return out
	}
	return nil
}

// dispatchInputs builds the workflow_dispatch inputs map per omitRunObjectInput (§2.4,
// FROZEN §13). Mode B (true, modern) passes only the run URL + sensitive system tokens +
// conditional endpoint; Mode A (false, legacy) passes exactly one input, the base64-JSON of
// the sanitized run object.
func dispatchInputs(run decryptedRun, impl meshapi.GithubImplementation) (map[string]string, error) {
	if impl.OmitRunObjectInput {
		return modeBInputs(run)
	}
	payload, err := modeARunObject(run)
	if err != nil {
		return nil, err
	}
	return map[string]string{inputKeyRunObject: payload}, nil
}

// modeBInputs is the Mode-B table (§2.4, keep-as-unit): buildingBlockRunUrl (required),
// MESHSTACK_API_TOKEN / MESHSTACK_RUN_TOKEN (iff an input with that exact key exists), and
// MESHSTACK_ENDPOINT (iff MESHSTACK_API_TOKEN was passed AND a MESHSTACK_ENDPOINT input
// exists). Regular user inputs are never dispatch inputs.
func modeBInputs(run decryptedRun) (map[string]string, error) {
	self := run.selfHref()
	if self == "" {
		uuid := ""
		if run.details != nil {
			uuid = run.details.Metadata.Uuid
		}
		//nolint:staticcheck // ST1005: frozen Kotlin IllegalStateException message, ported byte-identically (§7.11).
		return nil, fmt.Errorf("No self link found for building block run %s", uuid)
	}

	byKey := make(map[string]runInput, len(run.inputs))
	for _, in := range run.inputs {
		byKey[in.Key] = in
	}

	out := map[string]string{inputKeyRunUrl: self}
	if in, ok := byKey[inputKeyApiToken]; ok {
		out[inputKeyApiToken] = valueToString(in.Value)
	}
	if in, ok := byKey[inputKeyRunToken]; ok {
		out[inputKeyRunToken] = valueToString(in.Value)
	}
	if _, apiTokenPassed := out[inputKeyApiToken]; apiTokenPassed {
		if in, ok := byKey[inputKeyEndpoint]; ok {
			out[inputKeyEndpoint] = valueToString(in.Value)
		}
	}
	return out, nil
}

// ---- Mode A outbound payload struct (§4.4): the explicit field set mirroring the Kotlin
// DTO graph, with the implementation stripped to only its type discriminator. Structural
// omission replaces the Jackson @JsonIgnore mixin (umbrella §7.13). Parity is asserted at
// parsed-JSON level (null ≡ absent, 06A §16.4) — field ORDER and null-vs-absent do not
// matter; the field SET, values and secret hygiene (G-P10) do.

type modeAOut struct {
	Kind       string     `json:"kind"`
	ApiVersion string     `json:"apiVersion"`
	Metadata   modeAMeta  `json:"metadata"`
	Spec       modeASpec  `json:"spec"`
	Status     string     `json:"status"`
	Links      modeALinks `json:"_links"`
}

type modeAMeta struct {
	Uuid string `json:"uuid"`
}

type modeASpec struct {
	RunNumber     int             `json:"runNumber"`
	BuildingBlock modeABlock      `json:"buildingBlock"`
	Definition    modeADefinition `json:"buildingBlockDefinition"`
	Behavior      string          `json:"behavior"`
	RunToken      string          `json:"runToken"`
}

type modeABlock struct {
	Uuid string         `json:"uuid"`
	Spec modeABlockSpec `json:"spec"`
}

type modeABlockSpec struct {
	DisplayName            string                           `json:"displayName"`
	WorkspaceIdentifier    string                           `json:"workspaceIdentifier"`
	ProjectIdentifier      string                           `json:"projectIdentifier"`
	FullPlatformIdentifier string                           `json:"fullPlatformIdentifier"`
	Inputs                 []runInput                       `json:"inputs"`
	ParentBuildingBlocks   []meshapi.ParentBuildingBlockDTO `json:"parentBuildingBlocks"`
}

type modeADefinition struct {
	Uuid string       `json:"uuid"`
	Spec modeADefSpec `json:"spec"`
}

type modeADefSpec struct {
	WorkspaceIdentifier string        `json:"workspaceIdentifier"`
	Version             int           `json:"version"`
	Implementation      modeAImplType `json:"implementation"`
}

// modeAImplType is the implementation stripped to ONLY the type discriminator (G-P10): no
// appPem/owner/appId/branch/workflow fields, and no ciphertext of them, ever leave here.
type modeAImplType struct {
	Type string `json:"type"`
}

type modeALinks struct {
	Self             *meshapi.LinkDTO `json:"self,omitempty"`
	RegisterSource   *meshapi.LinkDTO `json:"registerSource,omitempty"`
	UpdateSource     *meshapi.LinkDTO `json:"updateSource,omitempty"`
	MeshstackBaseUrl *meshapi.LinkDTO `json:"meshstackBaseUrl,omitempty"`
}

// modeARunObject serializes the sanitized run object to base64(JSON) (§2.4). The decrypted
// inputs (json.Number fidelity, decrypted sensitive values), runToken and _links are
// included — the latter two are the legacy callback mechanism, NOT a leak (flag §16.4).
func modeARunObject(run decryptedRun) (string, error) {
	d := run.details
	if d == nil {
		return "", fmt.Errorf("run details are required for the buildingBlockRun payload")
	}

	implType, err := d.Spec.Definition.Spec.GetImplementationType()
	if err != nil {
		return "", fmt.Errorf("determining implementation type: %w", err)
	}

	out := modeAOut{
		Kind:       d.Kind,
		ApiVersion: d.ApiVersion,
		Metadata:   modeAMeta{Uuid: d.Metadata.Uuid},
		Spec: modeASpec{
			RunNumber: d.Spec.RunNumber,
			BuildingBlock: modeABlock{
				Uuid: d.Spec.BuildingBlock.Uuid,
				Spec: modeABlockSpec{
					DisplayName:            d.Spec.BuildingBlock.Spec.DisplayName,
					WorkspaceIdentifier:    d.Spec.BuildingBlock.Spec.WorkspaceIdentifier,
					ProjectIdentifier:      d.Spec.BuildingBlock.Spec.ProjectIdentifier,
					FullPlatformIdentifier: d.Spec.BuildingBlock.Spec.FullPlatformIdentifier,
					Inputs:                 inputsOrEmpty(run.inputs),
					ParentBuildingBlocks:   parentsOrEmpty(d.Spec.BuildingBlock.Spec.ParentBuildingBlocks),
				},
			},
			Definition: modeADefinition{
				Uuid: d.Spec.Definition.Uuid,
				Spec: modeADefSpec{
					WorkspaceIdentifier: d.Spec.Definition.Spec.WorkspaceIdentifier,
					Version:             d.Spec.Definition.Spec.Version,
					Implementation:      modeAImplType{Type: string(implType)},
				},
			},
			Behavior: d.Spec.Behavior,
			RunToken: d.Spec.RunToken,
		},
		Status: d.Status,
		Links:  modeALinksFrom(d.Links),
	}

	buf, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("marshaling buildingBlockRun payload: %w", err)
	}
	return base64.StdEncoding.EncodeToString(buf), nil
}

// modeALinksFrom copies only non-empty (href-bearing) links, so an absent link stays absent
// (null ≡ absent, §16.4) rather than serializing an empty {"href":""}.
func modeALinksFrom(l meshapi.LinksDTO) modeALinks {
	pick := func(link meshapi.LinkDTO) *meshapi.LinkDTO {
		if link.Href == "" {
			return nil
		}
		cp := link
		return &cp
	}
	return modeALinks{
		Self:             pick(l.Self),
		RegisterSource:   pick(l.RegisterSource),
		UpdateSource:     pick(l.UpdateSource),
		MeshstackBaseUrl: pick(l.MeshstackBaseUrl),
	}
}

// inputsOrEmpty returns a non-nil slice so an empty input list serializes as [] not null
// (matching the Kotlin "inputs" : [ ]).
func inputsOrEmpty(in []runInput) []runInput {
	if in == nil {
		return []runInput{}
	}
	return in
}

func parentsOrEmpty(in []meshapi.ParentBuildingBlockDTO) []meshapi.ParentBuildingBlockDTO {
	if in == nil {
		return []meshapi.ParentBuildingBlockDTO{}
	}
	return in
}

// valueToString renders an input value the way the Kotlin `value.toString()` does for the
// Mode-B tokens and sensitive-input decryption input: json.Number and string pass through
// as their literal text, other scalars via fmt.Sprint.
func valueToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}
