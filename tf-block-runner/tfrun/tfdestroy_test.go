package tfrun

import (
	"testing"

	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/stretchr/testify/assert"
)

func Test_DestroyOptions_SuppressForgetErrorsOnlyForSupportingTofu(t *testing.T) {
	suppress := []tfexec.DestroyOption{tfexec.Dir("-suppress-forget-errors")}

	assert.Equal(t, suppress, destroyOptions("1.12.0"))
	assert.Equal(t, suppress, destroyOptions("1.13.2"))
	assert.Empty(t, destroyOptions("1.11.5"), "OpenTofu before 1.12 rejects the flag")
	assert.Empty(t, destroyOptions(DEFAULT_TF_VER), "Terraform has no such flag")
}
