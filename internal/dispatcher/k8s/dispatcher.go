//go:build k8s

package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"uuid"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	batchinformers "k8s.io/client-go/informers/batch/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/internal/executor"
	"github.com/meshcloud/building-block-runner/internal/metrics"
)

type Dispatcher struct {
	cfg             config.Config
	clientset       kubernetes.Interface
	tokens          workloadIdentityTokens
	fetchOidcIssuer func(context.Context) (string, error)
	factory         informers.SharedInformerFactory
	jobs            batchinformers.JobInformer
	stopped         <-chan struct{}
	finished        chan dispatcher.Finished

	// The informer's event handler alone uses running and reported.
	running  map[uuid.UUID]bool
	reported map[uuid.UUID]bool
}

func New(ctx context.Context, cfg config.Config) (dispatcher.Dispatcher, error) {
	if err := cfg.ValidateK8s(); err != nil {
		return nil, err
	}
	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("kubernetes config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	return newDispatcher(ctx, cfg, clientset)
}

func newDispatcher(ctx context.Context, cfg config.Config, clientset kubernetes.Interface) (*Dispatcher, error) {
	d := &Dispatcher{
		cfg:       cfg,
		clientset: clientset,
		tokens:    newWorkloadIdentityTokens(cfg.K8s.Namespace),
		fetchOidcIssuer: func(ctx context.Context) (string, error) {
			return fetchOidcIssuer(ctx, clientset)
		},
		stopped:  ctx.Done(),
		finished: make(chan dispatcher.Finished),
		running:  map[uuid.UUID]bool{},
		reported: map[uuid.UUID]bool{},
	}
	if err := d.watchJobs(ctx); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Dispatcher) Finished() <-chan dispatcher.Finished {
	return d.finished
}

func (d *Dispatcher) Dispatch(ctx context.Context, run dispatcher.Run, wifToken string) error {
	runnerType, err := executor.RunnerTypeOf(run)
	if err != nil {
		return err
	}
	if !d.cfg.Dispatcher.ForTypes.Contains(runnerType) {
		return fmt.Errorf("this runner does not run %s runs", runnerType)
	}
	objects, err := d.newRunObjects(run, runnerType, wifToken)
	if err != nil {
		return err
	}

	jobs := d.clientset.BatchV1().Jobs(d.cfg.K8s.Namespace)
	if _, err = jobs.Get(ctx, objects.job.Name, metav1.GetOptions{}); err == nil {
		slog.InfoContext(ctx, "job already exists", "dispatcher", "k8s", "job", objects.job.Name)
		return nil
	}
	if err = d.createServiceAccount(ctx, objects.serviceAccount); err != nil {
		return err
	}
	if err = d.createSecret(ctx, objects.secret); err != nil {
		return err
	}
	job, err := createRetrying(ctx, jobs.Create, objects.job)
	// The create that failed transiently may have gone through.
	if apierrors.IsAlreadyExists(err) {
		job, err = jobs.Get(ctx, objects.job.Name, metav1.GetOptions{})
	}
	if err != nil {
		_ = d.clientset.CoreV1().Secrets(d.cfg.K8s.Namespace).Delete(ctx, objects.secret.Name, metav1.DeleteOptions{})
		return fmt.Errorf("create job %s: %w", objects.job.Name, err)
	}
	if err := d.ownSecret(ctx, objects.secret.Name, job); err != nil {
		slog.WarnContext(ctx, "secret outlives its job", "dispatcher", "k8s", "secret", objects.secret.Name, "error", err)
	}
	slog.InfoContext(ctx, "created job", "dispatcher", "k8s", "job", job.Name)
	return nil
}

// createServiceAccount keeps an existing account, as all runs of a building block definition share it.
func (d *Dispatcher) createServiceAccount(ctx context.Context, serviceAccount *corev1.ServiceAccount) error {
	_, err := createRetrying(ctx, d.clientset.CoreV1().ServiceAccounts(d.cfg.K8s.Namespace).Create, serviceAccount)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		metrics.ServiceAccountErrors.Inc()
		return fmt.Errorf("create service account %s: %w", serviceAccount.Name, err)
	}
	if err == nil {
		metrics.ServiceAccountsCreated.Inc()
	}
	return nil
}

// createSecret updates a Secret of an earlier dispatch of the run, so that the Job never mounts an old token.
func (d *Dispatcher) createSecret(ctx context.Context, secret *corev1.Secret) error {
	secrets := d.clientset.CoreV1().Secrets(d.cfg.K8s.Namespace)
	_, err := createRetrying(ctx, secrets.Create, secret)
	if apierrors.IsAlreadyExists(err) {
		_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("create secret %s: %w", secret.Name, err)
	}
	return nil
}

// ownSecret lets Kubernetes delete the Secret with its Job.
func (d *Dispatcher) ownSecret(ctx context.Context, name string, job *batchv1.Job) error {
	secrets := d.clientset.CoreV1().Secrets(d.cfg.K8s.Namespace)
	secret, err := secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	secret.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(job, batchv1.SchemeGroupVersion.WithKind("Job"))}
	_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	return err
}
