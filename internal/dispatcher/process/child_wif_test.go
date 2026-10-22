package process

import (
	"os"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_WriteWifToken_KeepsTheTokenOfAConcurrentRunOfTheBuildingBlock(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	require.NoError(t, err)
	buildingBlockUuid := uuid.New().String()
	first, second := &child{run: newRun(), root: root}, &child{run: newRun(), root: root}
	first.run.Spec.BuildingBlock.Uuid, second.run.Spec.BuildingBlock.Uuid = buildingBlockUuid, buildingBlockUuid

	_, err = first.writeWifToken("first")
	require.NoError(t, err)
	tokenFile, err := second.writeWifToken("second")
	require.NoError(t, err)
	first.removeDirs(t.Context())

	token, err := os.ReadFile(tokenFile)
	require.NoError(t, err)
	assert.Equal(t, "second", string(token))
}
