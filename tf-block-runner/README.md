# tf-block-runner

How to run and test locally.

## Run

```bash
go run .
```

Run with SSH known hosts:

```bash
SSH_KNOWN_HOSTS=./resources/known_hosts go run .
```

## Test

```bash
go test ./...
go test -v ./tfrun
```
