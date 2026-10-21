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
		// parse root, look for "terraform"
		terraformBlocks, _, diags := srcFile.Body.PartialContent(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{{Type: "terraform"}},
		})
		if diags.HasErrors() || terraformBlocks == nil {
			return false, "", diags
		}
		// parse "terraform" terraformBlock, look for "backend"
		for _, terraformBlock := range terraformBlocks.Blocks {
			backendBlocks, _, diags := terraformBlock.Body.PartialContent(&hcl.BodySchema{
				Blocks: []hcl.BlockHeaderSchema{{
					Type:       "backend",
					LabelNames: []string{"type"},
				}},
			})
			if diags.HasErrors() || backendBlocks == nil {
				return false, "", diags
			}
			for _, backendBlock := range backendBlocks.Blocks {
				if backendBlock.Type == "backend" {
					return true, srcFile.Filename, nil
				}
			}
		}
	}
	return false, "", nil
}
