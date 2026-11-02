package github

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// GitHub refuses an app token that lives longer than 10 minutes, and the clocks may differ.
	appJwtLifetime = 9 * time.Minute
	clockSkew      = time.Minute
)

func appJwt(appId, appKey string, now time.Time) (string, error) {
	key, err := parseAppKey(appKey)
	if err != nil {
		return "", err
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
	signature, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign the GitHub App token: %w", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseAppKey(appKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(appKey)))
	if block == nil {
		return nil, errors.New("the GitHub App key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the GitHub App key is neither a PKCS#1 nor a PKCS#8 private key: %w", err)
	}
	key, isRsa := parsed.(*rsa.PrivateKey)
	if !isRsa {
		return nil, fmt.Errorf("the GitHub App key is a %T, GitHub signs with RSA keys only", parsed)
	}
	return key, nil
}
