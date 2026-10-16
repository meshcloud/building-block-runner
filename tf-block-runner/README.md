# tf-block-runner

The tf runner image runs `cmd/runner` for the runner type `tf`. Run it locally from the repository root:

```bash
RUNNER_CONFIG_FILE=tf-block-runner/runner-config.yml go run -tags tf ./cmd/runner
```

Run with SSH known hosts:

```bash
SSH_KNOWN_HOSTS=tf-block-runner/resources/known_hosts RUNNER_CONFIG_FILE=tf-block-runner/runner-config.yml go run -tags tf ./cmd/runner
```

## Test

```bash
go test ./tf-block-runner/...
```
