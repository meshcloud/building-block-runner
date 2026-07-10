package gitlab

import (
	"errors"
	"net/url"
	"strings"
)

// sanitizeBaseUrl reproduces UrlSanitizerService.kt:8-20: trim whitespace, drop exactly
// one trailing slash, error on empty. This is a package-local helper, not a shared
// package (umbrella §4 row 13 -- 12 lines does not earn a package, P3); azdevops/github
// port the identical Kotlin behavior independently.
//
// Go additionally validates the result parses as a URL (url.Parse), replacing the
// deferred failure OkHttp's toHttpUrl() would raise at request-build time -- same row-C
// ("internal error") outcome, just surfaced one step earlier.
func sanitizeBaseUrl(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("URL should not be empty")
	}
	trimmed = strings.TrimSuffix(trimmed, "/")

	if _, err := url.Parse(trimmed); err != nil {
		return "", err
	}
	return trimmed, nil
}
