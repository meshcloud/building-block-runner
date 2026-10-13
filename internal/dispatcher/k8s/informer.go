//go:build k8s

package k8s

import (
	"context"
	"fmt"
	"log"
	"uuid"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/meshcloud/building-block-runner/internal/config"
	"github.com/meshcloud/building-block-runner/internal/dispatcher"
)

// annotationFailureReported keeps a later process from reporting a failed Job again.
const annotationFailureReported = "meshcloud.io/failure-reported"

// start returns once the status channel holds a not-ended status for every Job that is running.
func start(ctx context.Context, cfg config.Config, clientset kubernetes.Interface, oidcIssuer string, logger *log.Logger) (*Dispatcher, error) {
	d := newDispatcher(cfg, clientset, oidcIssuer, logger)
	factory := informers.NewSharedInformerFactoryWithOptions(clientset, 0,
		informers.WithNamespace(cfg.K8s.Namespace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = labelRunnerId + "=" + cfg.Uuid.String()
		}),
	)
	jobs := factory.Batch().V1().Jobs()
	informer := jobs.Informer()
	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		factory.Shutdown()
		return nil, fmt.Errorf("the Jobs of runner %s did not sync: %w", cfg.Uuid, context.Cause(ctx))
	}

	existing, err := jobs.Lister().List(labels.Everything())
	if err != nil {
		factory.Shutdown()
		return nil, err
	}
	d.status = make(chan dispatcher.RunStatus, len(existing))
	for _, job := range existing {
		if runUuid, ok := d.runUuid(job); ok && !isFinished(job) {
			d.running[runUuid] = true
			d.status <- dispatcher.RunStatus{RunUuid: runUuid}
		}
	}

	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { d.observe(ctx, obj) },
		UpdateFunc: func(_, obj any) { d.observe(ctx, obj) },
		DeleteFunc: func(obj any) { d.observeDeleted(ctx, obj) },
	}); err != nil {
		factory.Shutdown()
		return nil, err
	}
	context.AfterFunc(ctx, func() {
		factory.Shutdown()
		close(d.status)
	})
	return d, nil
}

func (d *Dispatcher) observe(ctx context.Context, obj any) {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return
	}
	runUuid, ok := d.runUuid(job)
	if !ok {
		return
	}
	switch failure := failureOf(job); {
	case failure != nil && job.Annotations[annotationFailureReported] == "" && !d.reported[runUuid]:
		delete(d.running, runUuid)
		if d.send(ctx, dispatcher.RunStatus{RunUuid: runUuid, Ended: true, Err: failure}) {
			d.reported[runUuid] = true
		}
	case isFinished(job) && d.running[runUuid]:
		delete(d.running, runUuid)
		d.send(ctx, dispatcher.RunStatus{RunUuid: runUuid, Ended: true})
	case !isFinished(job) && !d.running[runUuid]:
		d.running[runUuid] = true
		d.send(ctx, dispatcher.RunStatus{RunUuid: runUuid})
	}
}

// observeDeleted frees the capacity of a Job that was deleted while it ran.
func (d *Dispatcher) observeDeleted(ctx context.Context, obj any) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	d.observe(ctx, obj)
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return
	}
	if runUuid, ok := d.runUuid(job); ok && d.running[runUuid] {
		delete(d.running, runUuid)
		d.send(ctx, dispatcher.RunStatus{RunUuid: runUuid, Ended: true})
	}
}

func (d *Dispatcher) send(ctx context.Context, status dispatcher.RunStatus) bool {
	select {
	case d.status <- status:
		return true
	case <-ctx.Done():
		return false
	}
}

func (d *Dispatcher) FailureReported(ctx context.Context, runUuid uuid.UUID) {
	name := jobName(runUuid.String())
	patch := fmt.Appendf(nil, `{"metadata":{"annotations":{%q:"true"}}}`, annotationFailureReported)
	_, err := d.clientset.BatchV1().Jobs(d.cfg.K8s.Namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		d.logger.Printf("A later process may report job %s again, annotating it failed: %v", name, err)
	}
}

func (d *Dispatcher) runUuid(job *batchv1.Job) (uuid.UUID, bool) {
	runUuid, err := uuid.Parse(job.Labels[labelRunId])
	if err != nil {
		d.logger.Printf("Job %s has no run uuid in label %s: %v", job.Name, labelRunId, err)
		return uuid.UUID{}, false
	}
	return runUuid, true
}

func failureOf(job *batchv1.Job) error {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return fmt.Errorf("the Kubernetes Job %s failed: %s: %s", job.Name, condition.Reason, condition.Message)
		}
	}
	return nil
}

func isFinished(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if (condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed) && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
