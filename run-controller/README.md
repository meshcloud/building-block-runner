# run-controller

How to run and test locally.

## Local Development Prerequisites

1. **Start minikube** and ensure it is the active kubectl context:

   ```bash
   minikube start
   kubectl config use-context minikube
   ```

2. **Add the minikube host entry** to `/etc/hosts` so the controller and runner pods can reach meshfed running on your machine:

   ```bash
   echo "127.0.0.1 host.minikube.internal" | sudo tee -a /etc/hosts
   ```

3. **Configure `runner-config.yml`** — the default config at `runner-config.yml` is pre-configured for local development with `api.url: http://host.minikube.internal:8080`.

## Run

```bash
go run .
```

Or from the repo root:

```bash
make start-run-controller
```

## Test

```bash
go test ./...
go test -v ./controller
```
