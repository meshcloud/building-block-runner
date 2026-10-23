package github

import (
	gocrypto "crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"time"

	"github.com/meshcloud/building-block-runner/v2/internal/crypto"
)

const (
	// GitHub refuses an app token that lives longer than 10 minutes, and the clocks may differ.
	appJwtLifetime = 9 * time.Minute
	clockSkew      = time.Minute
)

func appJwt(appId, appKey string, now time.Time) (string, error) {
	key, err := crypto.ParsePrivateKey([]byte(appKey))
	if err != nil {
		return "", fmt.Errorf("GitHub App key: %w", err)
	}
	claims, err := json.Marshal(struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}{now.Add(-clockSkew).Unix(), now.Add(appJwtLifetime).Unix(), appId})
	if err != nil {
		return "", err
	}
	signed := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(nil, key, gocrypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign the GitHub App token: %w", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
