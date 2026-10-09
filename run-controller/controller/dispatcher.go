package controller

import (
	"fmt"
	"log"
	"os"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
)

type Dispatcher interface {
	Dispatch(run *meshapi.RunDetailsDTO, decryptedRunJsonBase64 string, runnerType string) error
	CountActive() (int, error)
}

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

func NewKubernetesDispatcher() Dispatcher {
	k8sClient, err := newKubernetesClient(AppConfig.Namespace, log.New(os.Stdout, "[K8S-CLIENT] ", log.LstdFlags))
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}
	return kubernetesDispatcher{jobs: k8sClient, metrics: NewMetricsCollector()}
}

func (d kubernetesDispatcher) Dispatch(run *meshapi.RunDetailsDTO, decryptedRunJsonBase64 string, runnerType string) error {
	jobSpec, ok := AppConfig.Implementations[runnerType]
	if !ok {
		return &NoHandlerError{RunnerType: runnerType}
	}
	return d.jobs.CreateRunnerJob(run.GetRunInfo(), decryptedRunJsonBase64, runnerType, &jobSpec, d.metrics)
}

func (d kubernetesDispatcher) CountActive() (int, error) {
	return d.jobs.CountActiveJobs()
}
