# run-controller

The run-controller image runs `cmd/runner` built with `-tags k8s`, which starts a Kubernetes Job per run. The Job runs
the runner image of its runner type on the run that `RUNNER_RUN_YML` names.

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

The command above skips the tests that need a real Keycloak. These tests start Keycloak in Docker, so Docker must be
running. If Docker does not use `/var/run/docker.sock` (for example with Colima), set `DOCKER_HOST` (see the
[Testcontainers configuration](https://golang.testcontainers.org/features/configuration/)). In CI, the step
"Test against Keycloak" runs them, because GitHub's Ubuntu runners have Docker.

```bash
task test:keycloak                          # Keycloak 26.8.1
task test:keycloak KEYCLOAK_VERSION=26.4.7  # another version
```
