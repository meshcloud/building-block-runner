package config

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestSingleRunMode(t *testing.T) {
	cases := []struct {
		name          string
		executionMode string
		springProfile string
		want          bool
	}{
		{"neither set", "", "", false},
		{"execution mode single-run", "single-run", "", true},
		{"execution mode other", "polling", "", false},
		{"spring profile kubernetes", "", "kubernetes", true},
		{"spring profile list contains kubernetes", "", "extra,kubernetes , other", true},
		{"spring profile other", "", "production", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(envExecutionMode, c.executionMode)
			t.Setenv(envSpringProfiles, c.springProfile)
			require.Equal(t, c.want, SingleRunMode(discardLog()))
		})
	}
}
