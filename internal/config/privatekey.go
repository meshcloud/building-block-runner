package config

import (
	"fmt"
	"log/slog"
	"os"
)

const (
	// envPrivateKeyFile is the highest-precedence private-key source, matching Kotlin's
	// PrivateKeyLoader and tf's existing binding.
	envPrivateKeyFile = "RUNNER_PRIVATE_KEY_FILE"
	// defaultPrivateKeyPath is the baked default mount the Kotlin runners fell back to
	// (PrivateKeyLoader.kt:8-24). Kept identical so an image that mounts a key there keeps
	// working without any config.
	defaultPrivateKeyPath = "/app/runner-private.pem"
)

// ResolvePrivateKey reproduces the Kotlin PrivateKeyLoader resolution order for the
// phase-6 personas (umbrella §4 row 9, 06A §6.5): env RUNNER_PRIVATE_KEY_FILE (non-blank)
// > the yaml privateKeyFile key (non-blank) > the default path /app/runner-private.pem; if
// the resolved file does not exist, fall back to the inline privateKey. A resolved file
// that exists but cannot be read is a hard error (P5) — a misconfigured key must fail
// fast, never silently fall through to an empty key.
//
// It is shared by the 06B–D personas (which decrypt) and ships here with the template even
// though the manual persona decrypts nothing (06A §6.5); tf's own key handling is
// untouched (umbrella §2 out-of-scope).
func ResolvePrivateKey(log *slog.Logger, fileKey, inlineKey string) (string, error) {
	path := defaultPrivateKeyPath
	source := "default path"
	if v := os.Getenv(envPrivateKeyFile); v != "" {
		path, source = v, envPrivateKeyFile
	} else if fileKey != "" {
		path, source = fileKey, "privateKeyFile"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Missing resolved file ⇒ inline key fallback (the Kotlin order). An empty inline
			// key here is the caller's problem to validate; it is not resolvable as a hard error.
			log.Info("private-key file not found; falling back to inline privateKey", "path", path, "source", source)
			return inlineKey, nil
		}
		return "", fmt.Errorf("reading private key file %s (%s): %w", path, source, err)
	}
	return string(data), nil
}
