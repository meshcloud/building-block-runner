package controller

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/meshcloud/building-block-runner/run-controller/keycloakwif"
)

const (
	staleWifClientAge             = 10 * time.Minute
	staleWifClientCleanupInterval = 10 * time.Minute
)

func newWifTokenIssuer(config *KeycloakWifConfig) *keycloakwif.Issuer {
	return keycloakwif.NewIssuer(keycloakwif.Config{
		Url:           config.Url,
		Realm:         config.Realm,
		Issuer:        config.Issuer,
		ClientId:      config.ClientId,
		ClientSecret:  config.ClientSecret,
		TokenLifetime: time.Duration(config.TokenLifetimeMinutes) * time.Minute,
	}, &http.Client{Timeout: wifTokenIssueTimeout}, log.New(os.Stdout, "[WIF-TOKEN] ", log.LstdFlags))
}

func deleteStaleWifClients(ctx context.Context, issuer *keycloakwif.Issuer, logger *log.Logger) {
	ticker := time.NewTicker(staleWifClientCleanupInterval)
	defer ticker.Stop()
	for {
		deleted, err := issuer.DeleteStaleClients(ctx, staleWifClientAge)
		if err != nil {
			logger.Printf("Failed to delete stale Keycloak clients: %v", err)
		} else if deleted > 0 {
			logger.Printf("Deleted %d stale Keycloak clients", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
