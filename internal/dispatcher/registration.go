package dispatcher

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/http"
	"github.com/meshcloud/building-block-runner/internal/http/meshapi"
)

const (
	registrationTimeout       = 10 * time.Minute
	registrationRetryInterval = 10 * time.Second
)

// Register updates the registration of a runner without workload identity.
func Register(ctx context.Context, cfg config.Config, logger *log.Logger, userAgent http.UserAgent) error {
	api, err := newRunnerClient(cfg, userAgent)
	if err != nil {
		return err
	}
	return register(ctx, cfg, logger, api, nil)
}

func register(ctx context.Context, cfg config.Config, logger *log.Logger, api meshapi.MeshRunnerClient, dispatcher Dispatcher) error {
	registration, err := newRegistration(cfg, dispatcher)
	if err != nil {
		return err
	}
	return updateRegistration(ctx, logger, api, registration)
}

// updateRegistration retries, as meshfed-api may still be starting.
func updateRegistration(ctx context.Context, logger *log.Logger, api meshapi.MeshRunnerClient, registration meshapi.MeshRunnerRegistration) error {
	ctx, cancel := context.WithTimeoutCause(ctx, registrationTimeout, fmt.Errorf("gave up after %s", registrationTimeout))
	defer cancel()
	runnerUuid := registration.Metadata.Uuid.String()
	for {
		err := api.UpdateRegistration(ctx, registration)
		if err == nil {
			registrationSuccess.WithLabelValues(runnerUuid).Inc()
			logger.Printf("Updated the registration of runner %s", runnerUuid)
			return nil
		}
		registrationErrors.WithLabelValues(runnerUuid, errorTypeUpdate).Inc()
		logger.Printf("Updating the registration failed, retrying in %s: %v", registrationRetryInterval, err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("update the registration of runner %s (%w): %w", runnerUuid, context.Cause(ctx), err)
		case <-time.After(registrationRetryInterval):
		}
	}
}

func newRegistration(cfg config.Config, dispatcher Dispatcher) (meshapi.MeshRunnerRegistration, error) {
	publicKey, err := cfg.Crypto.FirstPublicKey()
	if err != nil {
		return meshapi.MeshRunnerRegistration{}, err
	}
	registration := meshapi.MeshRunnerRegistration{
		Metadata: meshapi.MeshRunnerRegistrationMetadata{Uuid: cfg.Uuid, OwnedByWorkspace: cfg.Api.Auth.Workspace},
		Spec: meshapi.MeshRunnerRegistrationSpec{
			DisplayName:        cfg.Dispatcher.Register.DisplayName,
			PublicKey:          publicKey,
			ImplementationType: cfg.Dispatcher.ForTypes[0].ForApi(),
		},
	}
	if provider, ok := dispatcher.(WorkloadIdentityProvider); ok {
		registration.Spec.WorkloadIdentityFederation = provider.WorkloadIdentity()
	}
	return registration, nil
}
