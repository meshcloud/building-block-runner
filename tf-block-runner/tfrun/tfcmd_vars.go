package tfrun

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/gocty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

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
		valueJson, err := json.Marshal(value)
		if err == nil {
			// add the variable JSON-encoded as string as-is
			return diags.Extend(f.AddVariable(name, string(valueJson), AddVariableOptions{EncodeAsJsonString: false}))
		}
		return diags.
			Append(&hcl.Diagnostic{
				Severity: hcl.DiagWarning,
				Subject:  &hcl.Range{Filename: fmt.Sprintf("<var=%s>", name)},
				Summary:  "Failed to marshal value into JSON",
				Detail:   fmt.Sprintf("Cannot marshal generic value '%#v' to JSON, will fallback to fmt.Sprintf to string value: %s", value, err.Error()),
			}).
			Extend(f.AddVariable(name, fmt.Sprintf("%v", value), AddVariableOptions{EncodeAsJsonString: false}))
	}
	if value == nil {
		f.Body().SetAttributeValue(name, cty.NullVal(cty.String))
		return nil
	}

	impliedType, err := gocty.ImpliedType(value)
	if err != nil {
		// implying types does not always work when the value is the result of a JSON unmarshalling,
		// such as slice of strings, which has type []any{...}
		valueJson, err := json.Marshal(value)
		if err == nil {
			// Interpret the marshalled JSON as raw HCL and add this as the value
			return diags.Extend(f.AddRawVariable(name, string(valueJson), AddVariableOptions{EncodeAsJsonString: false}))
		}
		return diags.
			Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Subject:  &hcl.Range{Filename: fmt.Sprintf("<var=%s>", name)},
				Summary:  "Failed to marshal value into JSON after failing to imply type",
				Detail:   fmt.Sprintf("Cannot marshal generic value '%#v' to JSON: %s", value, err.Error()),
			})
	}

	ctyValue, err := gocty.ToCtyValue(value, impliedType)
	if err != nil {
		return diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Subject:  &hcl.Range{Filename: fmt.Sprintf("<var=%s>", name)},
			Summary:  "Cannot convert value to cty.Value",
			Detail:   fmt.Sprintf("The given value '%#v' cannot be converted: %s", value, err.Error()),
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
				Summary:  "Failed to parse raw HCL expression",
				Detail:   fmt.Sprintf("Cannot parse raw HCL '%s' as value expression, will fallback to string variable", rawExpression),
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

	expr, diags := hclsyntax.ParseExpression([]byte(rawExpression), fmt.Sprintf("<var=%s>", name), hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() || expr == nil {
		return fallbackUseRawHclAsStringValue(convertErrorDiagsToWarnings(diags))
	}

	if v, diags := expr.Value(nil); diags.HasErrors() {
		return fallbackUseRawHclAsStringValue(convertErrorDiagsToWarnings(diags))
	} else if options.EncodeAsJsonString {
		vJson, err := ctyjson.Marshal(v, v.Type())
		if err == nil {
			return diags.Extend(f.AddVariable(name, string(vJson), AddVariableOptions{EncodeAsJsonString: false}))
		}
		return fallbackUseRawHclAsStringValue(diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagWarning,
			Subject:  &hcl.Range{Filename: fmt.Sprintf("<var=%s>", name)},
			Summary:  "Cannot marshal raw HCL value to JSON",
			Detail:   fmt.Sprintf("The given raw HCL '%s' cannot be marshalled: %s", rawExpression, err.Error()),
		}))
	} else {
		f.Body().SetAttributeValue(name, v)
		return nil
	}
}
