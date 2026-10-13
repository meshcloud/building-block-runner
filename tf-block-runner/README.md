# tf-block-runner

The tf runner image runs `cmd/runner` for the runner type `tf`. Run it locally from the repository root:

```bash
RUNNER_CONFIG_FILE=tf-block-runner/runner-config.yml RUNNER_DISPATCHER_FOR_TYPES=tf go run ./cmd/runner
```

Run with SSH known hosts:

```bash
SSH_KNOWN_HOSTS=tf-block-runner/resources/known_hosts RUNNER_CONFIG_FILE=tf-block-runner/runner-config.yml RUNNER_DISPATCHER_FOR_TYPES=tf go run ./cmd/runner
```

## Test

```bash
go test ./tf-block-runner/...
```
