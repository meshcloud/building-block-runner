# run-controller

The run-controller image runs `cmd/runner` built with `-tags k8s`, which starts a Kubernetes Job per run.

## Run

```bash
task start:runner
```

## Debug the Kubernetes dispatcher

Use minikube only to investigate Kubernetes-specific bugs. run-controller runs on your machine and
starts the runner Jobs in minikube. `runner-config.yml` here already points at
`http://host.minikube.internal:8080`. Run from the repository root:

```bash
minikube start
kubectl config use-context minikube
echo "127.0.0.1 host.minikube.internal" | sudo tee -a /etc/hosts  # once
RUNNER_CONFIG_FILE=run-controller/runner-config.yml go run -tags k8s ./cmd/runner
```

## Test

```bash
task test
```
