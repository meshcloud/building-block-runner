# run-controller

The run-controller image runs `cmd/runner` built with `-tags k8s`, which starts a Kubernetes Job per run. The Job runs
the runner image of its runner type on the run that `RUNNER_RUN_YML` names.

## Run

```bash
task start:runner
```

## Debug the Kubernetes dispatcher

Use minikube only to investigate Kubernetes-specific bugs. The runner runs on your machine and starts the runner Jobs
in minikube, where a sidecar forwards each Job's `localhost:8080` to meshfed-api on your machine. Run from the
repository root:

```bash
task debug:minikube
```

## Test

```bash
task test
```
