package gitlab

import "testing"

// Test_SanitizeBaseUrl ports UrlSanitizerServiceTest (keep-as-unit -- a pure mapping with
// real decision surface, umbrella §5.2/D16 criterion): trims whitespace, drops exactly one
// trailing slash, errors on empty.
func Test_SanitizeBaseUrl(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"plain", "https://gitlab.example.com", "https://gitlab.example.com", false},
		{"trailing slash dropped", "https://gitlab.example.com/", "https://gitlab.example.com", false},
		{"only one trailing slash dropped", "https://gitlab.example.com//", "https://gitlab.example.com/", false},
		{"whitespace trimmed", "  https://gitlab.example.com  ", "https://gitlab.example.com", false},
		{"whitespace + trailing slash", "  https://gitlab.example.com/  ", "https://gitlab.example.com", false},
		{"empty errors", "", "", true},
		{"whitespace-only errors", "   ", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sanitizeBaseUrl(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("sanitizeBaseUrl(%q) = %q, nil; want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("sanitizeBaseUrl(%q) unexpected error: %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("sanitizeBaseUrl(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
