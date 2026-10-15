package config

import (
	"testing"

	"github.com/meshcloud/building-block-runner/internal/logctx/logctxtest"
)

func TestMain(m *testing.M) {
	logctxtest.Install()
	m.Run()
}
