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
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/meshcloud/building-block-runner/v2/internal/config"
	"github.com/meshcloud/building-block-runner/v2/internal/dispatcher"
	"github.com/meshcloud/building-block-runner/v2/internal/executor"
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
		AddFunc:    func(obj any) { d.observe(ctx, obj) },
		UpdateFunc: func(_, obj any) { d.observe(ctx, obj) },
		DeleteFunc: func(obj any) { d.observeDeleted(ctx, obj) },
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
		if runUuid, ok := d.runUuid(ctx, job); ok && !isFinished(job) {
			running = append(running, runUuid)
		}
	}
	return dispatcher.InitState{Running: running, WorkloadIdentity: d.workloadIdentity(ctx), KeycloakWifTokenPath: keycloakWifTokenPath}, nil
}

func (d *Dispatcher) observe(ctx context.Context, obj any) {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return
	}
	runUuid, ok := d.runUuid(ctx, job)
	if !ok {
		return
	}
	exitCode, finished := exitCodeOf(job)
	switch {
	case !finished:
		d.running[runUuid] = true
	case exitCode == executor.ExitUndelivered && !d.reported[runUuid]:
		delete(d.running, runUuid)
		if d.send(d.failed(job, runUuid, failureOf(job))) {
			d.reported[runUuid] = true
		}
	case d.running[runUuid]:
		delete(d.running, runUuid)
		d.send(finishedFrom(job, runUuid, exitCode))
	}
}

func (d *Dispatcher) observeDeleted(ctx context.Context, obj any) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	d.observe(ctx, obj)
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return
	}
	if runUuid, ok := d.runUuid(ctx, job); ok && d.running[runUuid] {
		delete(d.running, runUuid)
		d.send(d.failed(job, runUuid, fmt.Sprintf("The Kubernetes Job %s was deleted before it finished.", job.Name)))
	}
}

func (d *Dispatcher) send(finished dispatcher.Finished) bool {
	select {
	case d.finished <- finished:
		return true
	case <-d.stopped:
		return false
	}
}

func (d *Dispatcher) failed(job *batchv1.Job, runUuid uuid.UUID, failure string) dispatcher.Finished {
	finished := finishedFrom(job, runUuid, executor.ExitUndelivered)
	finished.Failure = failure
	finished.Reported = func(ctx context.Context) { d.markReported(ctx, runUuid) }
	return finished
}

func finishedFrom(job *batchv1.Job, runUuid uuid.UUID, exitCode int) dispatcher.Finished {
	started := job.CreationTimestamp.Time
	if job.Status.StartTime != nil {
		started = job.Status.StartTime.Time
	}
	runnerType, _ := config.ParseRunnerType(job.Labels[labelRunnerType])
	return dispatcher.Finished{RunUuid: runUuid, ExitCode: exitCode, Started: started, RunnerType: runnerType}
}

func (d *Dispatcher) markReported(ctx context.Context, runUuid uuid.UUID) {
	name := jobName(runUuid.String())
	patch := fmt.Appendf(nil, `{"metadata":{"annotations":{%q:"true"}}}`, annotationFailureReported)
	_, err := d.clientset.BatchV1().Jobs(d.cfg.K8s.Namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		slog.WarnContext(ctx, "annotating the job failed, a later process may report it again", "dispatcher", "k8s", "job", name, "error", err)
	}
}

func (*Dispatcher) runUuid(ctx context.Context, job *batchv1.Job) (uuid.UUID, bool) {
	runUuid, err := uuid.Parse(job.Labels[labelRunId])
	if err != nil {
		slog.WarnContext(ctx, "job has no run uuid in its label", "dispatcher", "k8s", "job", job.Name, "label", labelRunId, "error", err)
		return uuid.UUID{}, false
	}
	return runUuid, true
}

func exitCodeOf(job *batchv1.Job) (exitCode int, finished bool) {
	for _, condition := range job.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		switch {
		case condition.Type == batchv1.JobComplete:
			return executor.ExitStatusDelivered, true
		case condition.Type != batchv1.JobFailed:
			continue
		case condition.Reason == batchv1.JobReasonPodFailurePolicy, job.Annotations[annotationFailureReported] != "":
			return executor.ExitFailedDelivered, true
		default:
			return executor.ExitUndelivered, true
		}
	}
	return 0, false
}

func isFinished(job *batchv1.Job) bool {
	_, finished := exitCodeOf(job)
	return finished
}

func failureOf(job *batchv1.Job) string {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return fmt.Sprintf("The Kubernetes Job %s failed: %s: %s", job.Name, condition.Reason, condition.Message)
		}
	}
	return fmt.Sprintf("The Kubernetes Job %s failed.", job.Name)
}
