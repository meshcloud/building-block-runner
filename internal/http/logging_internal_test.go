package http

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogRenderingWaitsForTheSink(t *testing.T) {
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	rendered := 0
	body := loggedBody{&countingReader{counted: &rendered}}
	slog.DebugContext(t.Context(), "request", "body", body)
	assert.Zero(t, rendered, "the dropped record still rendered its body")

	slog.InfoContext(t.Context(), "request", "body", body)
	assert.Equal(t, 1, rendered, "the written record did not render its body")
}

func TestLoggedBodyRedactsCredentialsAndKeepsTheRest(t *testing.T) {
	const secret = "s3cr3t"
	tests := map[string]string{
		"api key login":   `{"clientId":"an-id","clientSecret":"` + secret + `","at":1234567890123456789}`,
		"login answer":    `{"access_token":"` + secret + `"}`,
		"nested secret":   `{"spec":{"config":{"clientSecret":{"plaintext":"` + secret + `"}}}}`,
		"oidc grant form": `grant_type=refresh_token&refresh_token=` + secret + `&client_id=an-id`,
		"oidc answer":     `{"access_token":"` + secret + `","refresh_token":"` + secret + `","scope":"openid"}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			rendered := loggedBody{bytes.NewBufferString(body)}.String()
			assert.NotContains(t, rendered, secret)
			assert.Contains(t, rendered, redactedValue)
		})
	}

	rendered := loggedBody{bytes.NewBufferString(tests["api key login"])}.String()
	assert.Contains(t, rendered, "an-id", "what is no credential stays")
	assert.Contains(t, rendered, "1234567890123456789", "a large integer stays exact")
}

type countingReader struct {
	counted *int
}

func (c *countingReader) Read([]byte) (int, error) { return 0, nil }

func (c *countingReader) String() string {
	*c.counted++
	return "counted"
}
