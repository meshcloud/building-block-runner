package main

import (
	"log/slog"
	"os"

	"github.com/meshcloud/building-block-runner/internal/config"
)

// readControllerConfig loads and validates the run-controller type's configuration
// (internal/config.LoadController); a load/validation failure is fatal at startup (only main
// wires the process exit).
func readControllerConfig(logger *slog.Logger) *config.ControllerConfig {
	cfg, err := config.LoadController(logger)
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	cfg.LogStartup(logger)
	return cfg
}
