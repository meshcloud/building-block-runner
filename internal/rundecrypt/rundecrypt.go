// Package rundecrypt wraps a dispatch.RunHandler so decryption happens once at the claim
// boundary -- the wrapped handler always sees plaintext Details/RawJson and stays key-oblivious,
// while the pinned decrypt-failure UX (secret.UnsupportedSensitiveTypeError, key-mismatch errors)
// still surfaces through the handler's own error path, unchanged from today's per-handler decrypt.
package rundecrypt

import (
	"context"
	"encoding/base64"

	"github.com/meshcloud/building-block-runner/internal/dispatch"
	"github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/secret"
)

// Wrap decorates inner so its Execute always receives a decrypted ClaimedRun. A nil dec means
// the run is already decrypted (e.g. single-run mode fed by an already-decrypting controller),
// so inner is returned unchanged rather than wrapped in a pass-through no-op.
func Wrap(inner dispatch.RunHandler, dec secret.Decryptor) dispatch.RunHandler {
	if dec == nil {
		return inner
	}
	return &decryptingHandler{inner: inner, dec: dec}
}

type decryptingHandler struct {
	inner dispatch.RunHandler
	dec   secret.Decryptor
}

func (h *decryptingHandler) Execute(ctx context.Context, cr dispatch.ClaimedRun) error {
	b64, err := meshapi.DecryptRunDetails(cr.RawJson, h.dec)
	if err != nil {
		// Returned verbatim so the run fails at the boundary, preserving
		// secret.UnsupportedSensitiveTypeError / key-mismatch UX.
		return err
	}

	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return err
	}
	parsed, err := meshapi.ParseRunDetails(raw)
	if err != nil {
		return err
	}

	return h.inner.Execute(ctx, dispatch.ClaimedRun{
		Id:      cr.Id,
		Type:    cr.Type,
		Details: parsed,
		RawJson: b64,
	})
}
