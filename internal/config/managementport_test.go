package config

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagementPort_NothingSet_ReturnsDefault(t *testing.T) {
	unsetEnv(t, "MANAGEMENT_PORT", "TEST_MGMT_PORT_ALIAS")

	got, err := ManagementPort(discardLogger(), 2112, EnvAlias{Var: "TEST_MGMT_PORT_ALIAS", Deprecated: true})
	require.NoError(t, err)
	assert.Equal(t, Port(2112), got)
}

func TestManagementPort_PrimarySet_Wins(t *testing.T) {
	t.Setenv("MANAGEMENT_PORT", "9100")
	t.Setenv("TEST_MGMT_PORT_ALIAS", "8100")

	got, err := ManagementPort(discardLogger(), 2112, EnvAlias{Var: "TEST_MGMT_PORT_ALIAS", Deprecated: true})
	require.NoError(t, err)
	assert.Equal(t, Port(9100), got)
}

func TestManagementPort_OnlyDeprecatedAliasSet_UsedAndWarns(t *testing.T) {
	unsetEnv(t, "MANAGEMENT_PORT")
	t.Setenv("TEST_MGMT_PORT_ALIAS", "8080")

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	got, err := ManagementPort(log, 8100, EnvAlias{Var: "TEST_MGMT_PORT_ALIAS", Deprecated: true})
	require.NoError(t, err)
	assert.Equal(t, Port(8080), got, "the container's PORT=8080 default must resolve unchanged (D10)")
	assert.Contains(t, buf.String(), "deprecated")
}

func TestManagementPort_NoAliasesConfigured_PrimaryStillWorks(t *testing.T) {
	// the run-controller persona passes zero aliases (PORT was never read by the
	// controller, D12 §4.3) -- MANAGEMENT_PORT alone must still resolve.
	t.Setenv("MANAGEMENT_PORT", "3000")

	got, err := ManagementPort(discardLogger(), 2112)
	require.NoError(t, err)
	assert.Equal(t, Port(3000), got)
}

func TestManagementPort_UnparseableValue_IsFatalError(t *testing.T) {
	t.Setenv("MANAGEMENT_PORT", "not-a-port")

	_, err := ManagementPort(discardLogger(), 2112)
	assert.Error(t, err)
}

func TestManagementPort_ZeroValue_IsFatalError(t *testing.T) {
	t.Setenv("MANAGEMENT_PORT", "0")

	_, err := ManagementPort(discardLogger(), 2112)
	assert.Error(t, err)
}

func TestManagementPort_OutOfUint16Range_IsFatalError(t *testing.T) {
	t.Setenv("MANAGEMENT_PORT", "99999")

	_, err := ManagementPort(discardLogger(), 2112)
	assert.Error(t, err)
}

func TestPort_Addr_FormatsBindAddress(t *testing.T) {
	assert.Equal(t, ":8100", Port(8100).Addr())
}
