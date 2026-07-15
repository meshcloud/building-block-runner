package tf

import (
	"fmt"
	"io/fs"
	"iter"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
)

type TfFile struct {
	*hcl.File
	Filename string
}

func ParseTerraformConfig(fsys fs.FS) iter.Seq2[TfFile, hcl.Diagnostics] {
	return func(yield func(TfFile, hcl.Diagnostics) bool) {
		entries, err := fs.ReadDir(fsys, ".")
		if err != nil {
			yield(TfFile{}, hcl.Diagnostics{{
				Severity: hcl.DiagError,
				Summary:  "Failed to read entries in Terraform config dir",
				Detail:   err.Error(),
			}})
			return // always stop iterating when entries can't be read
		}
		parser := hclparse.NewParser()
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			parseEntry := func(parseAs func([]byte, string) (*hcl.File, hcl.Diagnostics)) (TfFile, hcl.Diagnostics) {
				if content, err := fs.ReadFile(fsys, entry.Name()); err != nil {
					return TfFile{}, hcl.Diagnostics{{
						Severity: hcl.DiagError,
						Summary:  fmt.Sprintf("Failed to read file %s", entry.Name()),
						Detail:   err.Error(),
					}}
				} else {
					tfFile, diagnostics := parseAs(content, entry.Name())
					return TfFile{tfFile, entry.Name()}, diagnostics
				}
			}
			switch suffix := strings.ToLower(filepath.Ext(entry.Name())); suffix {
			case ".tf":
				if !yield(parseEntry(parser.ParseHCL)) {
					return
				}
			case ".json":
				if !yield(parseEntry(parser.ParseJSON)) {
					return
				}
			}
		}
	}
}

type VariableInput struct {
	Type string
}

type VariableInputs map[string]VariableInput

func ParseVariableInputs(fsys fs.FS) (VariableInputs, hcl.Diagnostics) {
	result := VariableInputs{}
	var errDiags hcl.Diagnostics
	for tfFile, diags := range ParseTerraformConfig(fsys) {
		if diags.HasErrors() {
			errDiags = errDiags.Extend(diags)
			continue
		}
		variableBlocks, _, diags := tfFile.Body.PartialContent(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{{
				Type:       "variable",
				LabelNames: []string{"name"},
			}},
		})
		if diags.HasErrors() || variableBlocks == nil {
			errDiags = errDiags.Extend(diags)
			continue
		}
		for _, variableBlock := range variableBlocks.Blocks {
			if len(variableBlock.Labels) != 1 {
				continue
			}
			varName := variableBlock.Labels[0]
			v := VariableInput{
				Type: "any",
			}
			const typeAttributeName = "type"
			variableBlockContent, _, diags := variableBlock.Body.PartialContent(&hcl.BodySchema{
				Attributes: []hcl.AttributeSchema{{Name: typeAttributeName}},
			})
			if diags.HasErrors() {
				errDiags = errDiags.Extend(diags)
				continue
			}
			if typeAttr, found := variableBlockContent.Attributes[typeAttributeName]; found {
				// Terraform's "type" argument is a type expression, not a value, so evaluating it as a value would fail.
				// We therefore preserve the raw HCL expression text here as the representation of the variable's type.
				v.Type = string(typeAttr.Expr.Range().SliceBytes(tfFile.Bytes))
			}
			result[varName] = v
		}
	}
	return result, errDiags
}
