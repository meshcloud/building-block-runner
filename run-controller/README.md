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
