package github

import (
	"errors"
	"strings"
)

// sanitizeBaseUrl reproduces the Kotlin UrlSanitizerService (§2.1.4, umbrella §4 row 13):
// trim surrounding whitespace, reject an empty result, then drop exactly ONE trailing "/".
// The GitHub base URL is per-run data (GHE support), not runner config, so it is sanitized
// on every processed run. Behavior pinned by Test_SanitizeBaseUrl (the GitHubClientFactory
// + UrlSanitizerService twins).
func sanitizeBaseUrl(url string) (string, error) {
	trimmed := strings.TrimSpace(url)
	if trimmed == "" {
		return "", errors.New("URL should not be empty")
	}
	return strings.TrimSuffix(trimmed, "/"), nil
}
