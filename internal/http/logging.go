package http

import (
	"bytes"
	"encoding"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"maps"
	gohttp "net/http"
	"os"
	"regexp"
	"slices"
	"strings"
)

// loggedHeaders redacts in both methods, because a JSON handler would otherwise encode the map itself.
type loggedHeaders gohttp.Header

var (
	_ fmt.Stringer           = loggedHeaders(nil)
	_ encoding.TextMarshaler = loggedHeaders(nil)
)

func (l loggedHeaders) MarshalText() ([]byte, error) {
	return []byte(l.String()), nil
}

func (l loggedHeaders) String() string {
	var lines []string
	for _, k := range slices.Sorted(maps.Keys(l)) {
		for _, v := range l[k] {
			if k == "Authorization" {
				v = redactedValue
			}
			lines = append(lines, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return strings.Join(lines, "\n")
}

// loggedBody implements fmt.Stringer, never slog.LogValuer, so that a dropped record never renders it.
type loggedBody struct {
	io.Reader
}

var (
	_ fmt.Stringer           = loggedBody{}
	_ encoding.TextMarshaler = loggedBody{}
)

func (l loggedBody) MarshalText() ([]byte, error) {
	return []byte(l.String()), nil
}

func (l loggedBody) String() string {
	switch body := l.Reader.(type) {
	case nil:
		return "<empty>"
	case *bytes.Buffer:
		return bytesToPrettyJson(body.Bytes())
	case *os.File:
		return "<file " + body.Name() + ">"
	default:
		return fmt.Sprintf("<unknown> %v", body)
	}
}

const secretNamePattern = `(?i:secret|token|password|plaintext)`

const redactedValue = "[REDACTED]"

var (
	secretName  = regexp.MustCompile(secretNamePattern)
	secretField = regexp.MustCompile(`([^&=\s]*` + secretNamePattern + `[^&=\s]*)=[^&\s]*`)
)

func bytesToPrettyJson(data []byte) string {
	if len(data) == 0 {
		return "<empty>"
	}
	if redacted, err := redactSecrets(data); err == nil {
		return redacted.String()
	}
	return fmt.Sprintf("<string,len=%d> %s", len(data), secretField.ReplaceAllString(string(data), "${1}="+redactedValue))
}

// redactSecrets rewrites the token stream, so that a large integer keeps its precision.
func redactSecrets(data []byte) (jsontext.Value, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	var indented bytes.Buffer
	encoder := jsontext.NewEncoder(&indented, jsontext.WithIndent("  "))
	secret := false
	for {
		if secret {
			secret = false
			if err := decoder.SkipValue(); err != nil {
				return nil, err
			}
			if err := encoder.WriteToken(jsontext.String(redactedValue)); err != nil {
				return nil, err
			}
			continue
		}
		switch decoder.PeekKind() {
		case 0:
			if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("cannot read the body as JSON: %w", err)
			}
			return bytes.TrimSuffix(indented.Bytes(), []byte("\n")), nil
		case '{', '}', '[', ']':
			token, err := decoder.ReadToken()
			if err != nil {
				return nil, err
			}
			if err := encoder.WriteToken(token); err != nil {
				return nil, err
			}
		default:
			container, read := decoder.StackIndex(decoder.StackDepth())
			value, err := decoder.ReadValue()
			if err != nil {
				return nil, err
			}
			secret = container == '{' && read%2 == 0 && secretName.Match(value)
			if err := encoder.WriteValue(value); err != nil {
				return nil, err
			}
		}
	}
}
