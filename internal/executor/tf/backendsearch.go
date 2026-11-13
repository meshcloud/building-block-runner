package tf

import (
	"io/fs"

	"github.com/hashicorp/hcl/v2"
)

func FindBackendConfig(fsys fs.FS) (bool, string, hcl.Diagnostics) {
	for srcFile, diags := range ParseTerraformConfig(fsys) {
		if diags.HasErrors() {
			return false, "", diags
		}
		terraformBlocks, _, diags := srcFile.Body.PartialContent(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{{Type: "terraform"}},
		})
		if diags.HasErrors() {
			return false, "", diags
		}
		for _, terraformBlock := range terraformBlocks.Blocks {
			backendBlocks, _, diags := terraformBlock.Body.PartialContent(&hcl.BodySchema{
				Blocks: []hcl.BlockHeaderSchema{{Type: "backend", LabelNames: []string{"type"}}},
			})
			if diags.HasErrors() {
				return false, "", diags
			}
			if len(backendBlocks.Blocks) > 0 {
				return true, srcFile.Filename, nil
			}
		}
	}
	return false, "", nil
}
