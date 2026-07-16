package github

import "testing"

// Test_SanitizeBaseUrl pins the UrlSanitizerService + GitHubClientFactory twins: trim, drop
// one trailing slash, reject empty.
func Test_SanitizeBaseUrl(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"plain", "https://api.github.com", "https://api.github.com", false},
		{"one-trailing-slash", "https://api.github.com/", "https://api.github.com", false},
		{"surrounding-whitespace", "  https://ghe.example.com/api/v3  ", "https://ghe.example.com/api/v3", false},
		{"only-one-slash-dropped", "https://api.github.com//", "https://api.github.com/", false},
		{"empty", "", "", true},
		{"whitespace-only", "   ", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := sanitizeBaseUrl(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("sanitizeBaseUrl(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}
