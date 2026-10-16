# run-controller

## Run

```bash
task start:runner
```

## Debug the Kubernetes dispatcher

Use minikube only to investigate Kubernetes-specific bugs. run-controller runs on your machine and
starts the runner Jobs in minikube. `runner-config.yml` already points at
`http://host.minikube.internal:8080`.

```bash
minikube start
kubectl config use-context minikube
echo "127.0.0.1 host.minikube.internal" | sudo tee -a /etc/hosts  # once
go run .
```

## Test

```bash
go test -tags inprocess ./...
```

The meshStack workload identity token needs a real Keycloak to test. This starts one in Docker and tests against it:

```bash
task test:keycloak                          # Keycloak 26.8.1
task test:keycloak KEYCLOAK_VERSION=26.4.7  # another version
```
