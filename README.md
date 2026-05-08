# Building Block Runner

- **run-controller**: orchestrates terraform execution in Kubernetes
- **tf-block-runner**: executes terraform plans and applies

See:
- [run-controller/README.md](run-controller/README.md)
- [tf-block-runner/README.md](tf-block-runner/README.md)

Go workspace note:
- This repository uses [go.work](go.work) to develop modules together.
- If module references change, run `go work sync` from the repository root.
- Common tasks are available via `make` (see [Makefile](Makefile)).
