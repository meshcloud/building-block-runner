# Run Controller - Local Development

## Prerequisites

- [Nix flake](../../flake.nix) environment (includes minikube, kubectl, and other dependencies)
- Docker
- `minikube start` (optionally `--memory=8192 --cpus=4`)

## Quick Start

```bash
# Controller runs on the host; runner Jobs spin up inside minikube
./gradlew :buildingblocks:run-controller:start

# Controller runs as a Pod inside minikube
./gradlew :buildingblocks:run-controller:startInCluster
```

Both modes use `http://host.minikube.internal:8080` as the meshStack API URL so status updates reach your locally running backend. Add `127.0.0.1 host.minikube.internal` to your `/etc/hosts`.

Configure credentials, runner UUIDs, and crypto keys in `application.yml`.

## In-Cluster Iteration

```bash
# Rebuild and redeploy only the run-controller after code changes
./gradlew :buildingblocks:run-controller:reloadRc

# Watch logs
kubectl logs -l app=run-controller -f

# Tear down
./gradlew :buildingblocks:run-controller:undeployFromCluster
```
