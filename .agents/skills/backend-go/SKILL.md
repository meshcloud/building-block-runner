---
name: backend-go
description: Go development conventions for the building-block-runner — load when working with any Go code in this repo (cmd/*, internal/*)
---
## Layout

One Go module rooted at the repo root (`go.mod` at root; no `go.work`, no per-runner-type
submodules). Entry points are `cmd/<type>/main.go` (wiring only); all domain/application
code lives under `internal/*`. See `docs/ARCHITECTURE.md` for the package map, the depguard
layering rules (D11: adapters never import their consumers; only `cmd/*` wires things
together), and the single logging stack (D15: `log/slog` everywhere, never stdlib `log`).

## Companion Files

- `testing.md` — write or adapt tests when adding or changing any code. Covers test
  structure, the libraries in use, and how to run the suite.

## Mandatory Steps After Every Change

```bash
task fmt    # golangci-lint fmt — the one formatter authority (supersedes go fmt/goimports)
task lint   # golangci-lint run (govet runs inside it — do not run go vet separately)
task test   # go test -race ./...
```

`task fmt`/`task lint`/`task test` all run at the repo root. Never skip formatting, lint, or
tests. Fix all failures before finishing; the coverage gate (`task coverage`) must stay green
too (see `docs/ARCHITECTURE.md` for thresholds/exclusions).
