package crypto

import (
	"regexp"
	"strings"
)

// pemMarkerPattern matches a full "-----BEGIN <TYPE>-----<body>-----END <TYPE>-----" block
// regardless of whether newlines separate the markers from the body -- the shape Kotlin's
// hand-rolled PrivateKeyLoader/MeshCertDecryptionService parser accepts (it strips the marker
// strings and every newline, then base64-decodes what remains) but Go's strict encoding/pem
// does not.
var pemMarkerPattern = regexp.MustCompile(`(?s)-----BEGIN ([A-Z0-9 ]+)-----(.*?)-----END ([A-Z0-9 ]+)-----`)

// pemLineLength is the conventional PEM body wrap width (RFC 7468 recommends 64).
const pemLineLength = 64

// normalizePEM re-wraps a PEM blob that Go's strict encoding/pem.Decode rejects (most
// commonly: no newline between the BEGIN marker and the base64 body, the single-line shape
// shipped in Kotlin-era runner-config.yml files) into a standard, decodable multi-line PEM.
// ok is false when the input does not even contain a BEGIN/END marker pair (nothing to
// normalize) or the BEGIN/END types disagree -- both left for the caller's existing
// "no valid PEM block found" error, not invented here.
func normalizePEM(raw []byte) (out []byte, ok bool) {
	m := pemMarkerPattern.FindSubmatch(raw)
	if m == nil {
		return nil, false
	}
	beginType, body, endType := string(m[1]), string(m[2]), string(m[3])
	if beginType != endType {
		return nil, false
	}

	body = strings.Join(strings.Fields(body), "") // drop every whitespace/newline byte

	var b strings.Builder
	b.WriteString("-----BEGIN " + beginType + "-----\n")
	for i := 0; i < len(body); i += pemLineLength {
		end := i + pemLineLength
		if end > len(body) {
			end = len(body)
		}
		b.WriteString(body[i:end])
		b.WriteByte('\n')
	}
	b.WriteString("-----END " + endType + "-----\n")

	return []byte(b.String()), true
}
