package tf

import (
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"slices"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/gocty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

// VarsFile never quotes a variable's value in a diagnostic, because diagnostics end up in the run log and the value may be a secret.
type VarsFile struct {
	*hclwrite.File
}

func NewEmptyVarsFile() VarsFile {
	return VarsFile{hclwrite.NewEmptyFile()}
}

type AddVariableOptions struct {
	EncodeAsJsonString bool
}

func (f VarsFile) VariableNames() []string {
	return slices.Collect(maps.Keys(f.Body().Attributes()))
}

func (f VarsFile) AddVariable(name string, value any, opts AddVariableOptions) (diags hcl.Diagnostics) {
	if opts.EncodeAsJsonString {
		valueJson, marshalErr := json.Marshal(value)
		if marshalErr == nil {
			// add the variable JSON-encoded as string as-is
			return diags.Extend(f.AddVariable(name, string(valueJson), AddVariableOptions{EncodeAsJsonString: false}))
		}
		return diags.
			Append(&hcl.Diagnostic{
				Severity: hcl.DiagWarning,
				Subject:  &hcl.Range{Filename: fmt.Sprintf("<var=%s>", name)},
				Summary:  "Failed to marshal value into JSON",
				Detail:   "Cannot marshal generic value to JSON, will fallback to fmt.Sprintf to string value: " + marshalErr.Error(),
			}).
			Extend(f.AddVariable(name, fmt.Sprintf("%v", value), AddVariableOptions{EncodeAsJsonString: false}))
	}
	if value == nil {
		f.Body().SetAttributeValue(name, cty.NullVal(cty.String))
		return nil
	}
	if integer, isBig := value.(*big.Int); isBig {
		f.Body().SetAttributeValue(name, cty.NumberVal(new(big.Float).SetInt(integer)))
		return nil
	}

	impliedType, err := gocty.ImpliedType(value)
	if err != nil {
		// implying types does not always work when the value is the result of a JSON unmarshalling,
		// such as slice of strings, which has type []any{...}
		valueJson, marshalErr := json.Marshal(value)
		if marshalErr == nil {
			// Interpret the marshalled JSON as raw HCL and add this as the value
			return diags.Extend(f.AddRawVariable(name, string(valueJson), AddVariableOptions{EncodeAsJsonString: false}))
		}
		return diags.
			Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Subject:  &hcl.Range{Filename: fmt.Sprintf("<var=%s>", name)},
				Summary:  "Failed to marshal value into JSON after failing to imply type",
				Detail:   "Cannot marshal generic value to JSON: " + marshalErr.Error(),
			})
	}

	ctyValue, err := gocty.ToCtyValue(value, impliedType)
	if err != nil {
		return diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Subject:  &hcl.Range{Filename: fmt.Sprintf("<var=%s>", name)},
			Summary:  "Cannot convert value to cty.Value",
			Detail:   "The given value cannot be converted: " + err.Error(),
		})
	}
	f.Body().SetAttributeValue(name, ctyValue)
	return nil
}

func (f VarsFile) AddRawVariable(name string, rawExpression string, options AddVariableOptions) hcl.Diagnostics {
	fallbackUseRawHclAsStringValue := func(diags hcl.Diagnostics) hcl.Diagnostics {
		return diags.
			Append(&hcl.Diagnostic{
				Severity: hcl.DiagWarning,
				Subject:  &hcl.Range{Filename: fmt.Sprintf("<var=%s>", name)},
				Summary:  "Failed to parse input as JSON or HCL",
				Detail:   "Cannot parse input as JSON or as HCL value expression, will fallback to string variable",
			}).
			Extend(f.AddVariable(name, rawExpression, AddVariableOptions{EncodeAsJsonString: false}))
	}

	convertErrorDiagsToWarnings := func(diags hcl.Diagnostics) hcl.Diagnostics {
		for _, diag := range diags {
			if diag.Severity == hcl.DiagError {
				diag.Severity = hcl.DiagWarning
			}
		}
		return diags
	}

	v, diags := parseJsonOrHcl(name, rawExpression)
	if diags.HasErrors() {
		return fallbackUseRawHclAsStringValue(convertErrorDiagsToWarnings(diags))
	}
	if !options.EncodeAsJsonString {
		f.Body().SetAttributeValue(name, v)
		return nil
	}
	//goland:noinspection GoDfaErrorMayBeNotNil // hcl.Diagnostics is checked with HasErrors, which the inspection does not follow.
	vJson, err := ctyjson.Marshal(v, v.Type())
	if err != nil {
		return fallbackUseRawHclAsStringValue(diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagWarning,
			Subject:  &hcl.Range{Filename: fmt.Sprintf("<var=%s>", name)},
			Summary:  "Cannot marshal raw HCL value to JSON",
			Detail:   "The given raw HCL cannot be marshalled: " + err.Error(),
		}))
	}
	return diags.Extend(f.AddVariable(name, string(vJson), AddVariableOptions{EncodeAsJsonString: false}))
}

// JSON goes first because HCL would read "${" and "%{" inside a JSON string as a template.
func parseJsonOrHcl(name string, raw string) (cty.Value, hcl.Diagnostics) {
	if json.Valid([]byte(raw)) {
		v, err := unmarshalJson([]byte(raw))
		if err != nil {
			return cty.NilVal, hcl.Diagnostics{{
				Severity: hcl.DiagError,
				Subject:  &hcl.Range{Filename: fmt.Sprintf("<var=%s>", name)},
				Summary:  "Cannot convert JSON to cty.Value",
				Detail:   "The given JSON cannot be converted: " + err.Error(),
			}}
		}
		return v, nil
	}

	expr, diags := hclsyntax.ParseExpression([]byte(raw), fmt.Sprintf("<var=%s>", name), hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		return cty.NilVal, diags
	}
	//goland:noinspection GoDfaErrorMayBeNotNil // hcl.Diagnostics is checked with HasErrors, which the inspection does not follow.
	v, diags := expr.Value(nil)
	if diags.HasErrors() {
		return cty.NilVal, diags
	}
	return v, diags
}

func unmarshalJson(raw []byte) (cty.Value, error) {
	impliedType, err := ctyjson.ImpliedType(raw)
	if err != nil {
		return cty.NilVal, err
	}
	return ctyjson.Unmarshal(raw, impliedType)
}
