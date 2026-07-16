package github

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/meshcloud/building-block-runner/internal/config"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(discard{}, nil)) }

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func Test_Config_Validate(t *testing.T) {
	fullApi := config.Api{Url: "http://mesh", Username: "u", Password: "p"}

	tests := []struct {
		name      string
		cfg       Config
		singleRun bool
		wantErr   string
	}{
		{"single-run-exempt", Config{}, true, ""},
		{"ok-polling", Config{BaseConfig: config.BaseConfig{Uuid: "u", Api: fullApi}, PrivateKey: "pem"}, false, ""},
		{"missing-uuid", Config{BaseConfig: config.BaseConfig{Api: fullApi}, PrivateKey: "pem"}, false, "uuid is required"},
		{"missing-api-url", Config{BaseConfig: config.BaseConfig{Uuid: "u", Api: config.Api{Username: "u", Password: "p"}}, PrivateKey: "pem"}, false, "api.url is required"},
		{"missing-auth", Config{BaseConfig: config.BaseConfig{Uuid: "u", Api: config.Api{Url: "http://mesh"}}, PrivateKey: "pem"}, false, "no authentication configured"},
		{"missing-private-key", Config{BaseConfig: config.BaseConfig{Uuid: "u", Api: fullApi}}, false, "a private key is required in polling mode"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate(tc.singleRun)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v; want substring %q", err, tc.wantErr)
			}
		})
	}
}
