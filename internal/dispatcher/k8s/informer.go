//go:build k8s

package k8s

import (
	"context"
	"fmt"
	"uuid"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/meshcloud/building-block-runner/internal/dispatcher"
)

// annotationFailureReported keeps a later process from reporting a failed Job again.
const annotationFailureReported = "meshcloud.io/failure-reported"

func (d *Dispatcher) watchJobs(ctx context.Context) error {
	d.factory = informers.NewSharedInformerFactoryWithOptions(d.clientset, 0,
		informers.WithNamespace(d.cfg.K8s.Namespace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = labelRunnerId + "=" + d.cfg.Uuid.String()
		}),
	)
	d.jobs = d.factory.Batch().V1().Jobs()
	if _, err := d.jobs.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    d.observe,
		UpdateFunc: func(_, obj any) { d.observe(obj) },
		DeleteFunc: d.observeDeleted,
	}); err != nil {
		return err
	}
	context.AfterFunc(ctx, func() {
		d.factory.Shutdown()
		close(d.finished)
	})
	return nil
}

func (d *Dispatcher) Init(ctx context.Context) (dispatcher.InitState, error) {
	d.factory.Start(d.stopped)
	if !cache.WaitForCacheSync(ctx.Done(), d.jobs.Informer().HasSynced) {
		return dispatcher.InitState{}, fmt.Errorf("the Jobs of runner %s did not sync: %w", d.cfg.Uuid, context.Cause(ctx))
	}
	existing, err := d.jobs.Lister().List(labels.Everything())
	if err != nil {
		return dispatcher.InitState{}, err
	}
	var running []uuid.UUID
	for _, job := range existing {
		if runUuid, ok := d.runUuid(job); ok && !isFinished(job) {
			running = append(running, runUuid)
		}
	}
	return dispatcher.InitState{Running: running, WorkloadIdentity: d.workloadIdentity(ctx)}, nil
}

func (d *Dispatcher) observe(obj any) {
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
		if d.send(dispatcher.RunFinished{RunUuid: runUuid, Err: failure}) {
			d.reported[runUuid] = true
		}
	case isFinished(job) && d.running[runUuid]:
		delete(d.running, runUuid)
		d.send(dispatcher.RunFinished{RunUuid: runUuid})
	case !isFinished(job):
		d.running[runUuid] = true
	}
}

func (d *Dispatcher) observeDeleted(obj any) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	d.observe(obj)
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return
	}
	if runUuid, ok := d.runUuid(job); ok && d.running[runUuid] {
		delete(d.running, runUuid)
		d.send(dispatcher.RunFinished{RunUuid: runUuid, Err: fmt.Errorf("the Kubernetes Job %s was deleted before it finished", job.Name)})
	}
}

func (d *Dispatcher) send(finished dispatcher.RunFinished) bool {
	select {
	case d.finished <- finished:
		return true
	case <-d.stopped:
		return false
	}
}

func (d *Dispatcher) FailureReported(ctx context.Context, runUuid uuid.UUID) {
	name := jobName(runUuid.String())
	patch := fmt.Appendf(nil, `{"metadata":{"annotations":{%q:"true"}}}`, annotationFailureReported)
	_, err := d.clientset.BatchV1().Jobs(d.cfg.K8s.Namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
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
