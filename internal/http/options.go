package http

import (
	"context"
	"encoding/json/v2"
	gohttp "net/http"
)

type (
	RequestOption func(opts *requestOptions)

	requestOptions struct {
		retryable        bool
		requestPayload   func() ([]byte, error)
		bodyFile         string
		requestModifiers []requestModifier
	}
	requestModifier func(req *gohttp.Request) error
)

// Retryable is the only way a method other than GET is ever retried.
func Retryable() RequestOption {
	return func(opts *requestOptions) {
		opts.retryable = true
	}
}

// retryableKey travels in the request context, which gohttp.Client also passes on to a redirect.
type retryableKey struct{}

func isRetryable(ctx context.Context) bool {
	retryable, _ := ctx.Value(retryableKey{}).(bool)
	return retryable
}

func appendRequestModifier(modifier requestModifier) RequestOption {
	return func(opts *requestOptions) {
		opts.requestModifiers = append(opts.requestModifiers, modifier)
	}
}

func WithAccept(accept string) RequestOption {
	return withHeader("Accept", accept)
}

func WithHeaders(header gohttp.Header) RequestOption {
	return appendRequestModifier(func(req *gohttp.Request) error {
		for key, values := range header {
			req.Header.Del(key)
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
		return nil
	})
}

// WithBody sends the bytes as they are, so the caller names their type in a Content-Type header.
func WithBody(body []byte) RequestOption {
	return func(opts *requestOptions) {
		if body == nil {
			return
		}
		opts.requestPayload = func() ([]byte, error) {
			return body, nil
		}
	}
}

// WithBodyFile sends the file as it is, so the caller names its type in a Content-Type header.
func WithBodyFile(path string) RequestOption {
	return func(opts *requestOptions) {
		opts.bodyFile = path
	}
}

func withHeader(key, value string) RequestOption {
	return appendRequestModifier(func(req *gohttp.Request) error {
		req.Header.Set(key, value)
		return nil
	})
}

// wireCompatibility keeps the output of encoding/json v1, which meshStack may read differently.
var wireCompatibility = json.JoinOptions(
	json.Deterministic(true),
	json.FormatNilSliceAsNull(true),
	json.FormatNilMapAsNull(true),
)

// WithJsonPayload sends and asks for contentType, which names a meshObject's kind and version.
func WithJsonPayload(payload any, contentType string) RequestOption {
	return func(opts *requestOptions) {
		if payload == nil {
			return
		}
		WithAccept(contentType)(opts)
		withHeader("Content-Type", contentType)(opts)
		opts.requestPayload = func() ([]byte, error) {
			return json.Marshal(payload, wireCompatibility)
		}
	}
}
