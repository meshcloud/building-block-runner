package keycloakwif

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

var allowedClaims = []string{"iss", "sub", "aud", "azp", "exp", "iat", "nbf", "jti", "typ", "scope"}

const lifetimeTolerance = 2 * time.Minute

func checkClaims(token, issuer, subject string, lifetime time.Duration) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return errors.New("token payload is not base64url")
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return errors.New("token payload is not a JSON object")
	}

	for claim := range claims {
		if !slices.Contains(allowedClaims, claim) {
			return fmt.Errorf("token has the unexpected claim %q", claim)
		}
	}
	if claims["iss"] != issuer {
		return fmt.Errorf("token iss is %v, want %s", claims["iss"], issuer)
	}
	if claims["sub"] != subject {
		return fmt.Errorf("token sub is %v, want %s", claims["sub"], subject)
	}
	if claims["azp"] != Audience {
		return fmt.Errorf("token azp is %v, want %s", claims["azp"], Audience)
	}
	if !isOnlyAudience(claims["aud"]) {
		return fmt.Errorf("token aud is %v, want %s", claims["aud"], Audience)
	}

	exp, ok := claims["exp"].(float64)
	if !ok {
		return errors.New("token has no exp claim")
	}
	iat, ok := claims["iat"].(float64)
	if !ok {
		return errors.New("token has no iat claim")
	}
	// Keycloak sets both from its own clock, so the run-controller's clock does not matter.
	if issued := time.Duration(exp-iat) * time.Second; issued < lifetime-lifetimeTolerance || issued > lifetime+lifetimeTolerance {
		return fmt.Errorf("token lifetime is %s, want %s", issued, lifetime)
	}
	return nil
}

func isOnlyAudience(aud any) bool {
	switch aud := aud.(type) {
	case string:
		return aud == Audience
	case []any:
		return len(aud) == 1 && aud[0] == Audience
	default:
		return false
	}
}
