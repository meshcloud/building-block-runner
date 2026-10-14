package controller

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/meshcloud/building-block-runner/run-controller/build"
)

type NoHandlerError struct {
	RunnerType string
}

func (e *NoHandlerError) Error() string {
	return fmt.Sprintf("no implementation handler configured for type '%s'", e.RunnerType)
}

type JobManager interface {
	CreateRunnerJob(runInfo meshapi.RunInfo, runJsonBase64 string, implType string, jobSpec *JobSpecTemplate, metrics *MetricsCollector) error
	CountActiveJobs() (int, error)
}

type kubernetesDispatcher struct {
	jobs    JobManager
	metrics *MetricsCollector
}

func NewKubernetesDispatcher(ctx context.Context) build.Dispatcher {
	k8sClient, err := newKubernetesClient(AppConfig.Namespace, log.New(os.Stdout, "[K8S-CLIENT] ", log.LstdFlags))
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}
	if keycloak := AppConfig.WorkloadIdentityFederation.Keycloak; keycloak != nil {
		issuer := newWifTokenIssuer(keycloak)
		k8sClient.wifTokens = issuer
		k8sClient.wifWarnings = newRunApiClient()
		go deleteStaleWifClients(ctx, issuer, log.New(os.Stdout, "[WIF-TOKEN] ", log.LstdFlags))
	}
	return kubernetesDispatcher{jobs: k8sClient, metrics: NewMetricsCollector()}
}

func (d kubernetesDispatcher) Dispatch(_ context.Context, run *meshapi.RunDetailsDTO, decryptedRunJsonBase64 string, runnerType string) error {
	jobSpec, ok := AppConfig.Implementations[runnerType]
	if !ok {
		return &NoHandlerError{RunnerType: runnerType}
	}
	return d.jobs.CreateRunnerJob(run.GetRunInfo(), decryptedRunJsonBase64, runnerType, &jobSpec, d.metrics)
}

func (d kubernetesDispatcher) CountActive() (int, error) {
	return d.jobs.CountActiveJobs()
}

// Jobs outlive the controller in the cluster.
func (d kubernetesDispatcher) Wait() {}
