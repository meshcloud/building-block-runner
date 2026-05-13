---
name: backend-go
description: Go development conventions for the tf-block-runner service — load when working with any Go code in `tf-block-runner/`
---
## Companion Files

- `testing.md` — Write or adapt tests when adding or changing any code in the tf-block-runner. This file provides guidelines on test structure, libraries in use, and how to run tests effectively.

## Mandatory Steps After Every Change

```bash
cd tf-block-runner
goimports -w ./...
go build ./...
go vet ./...
go test ./...
```

Never skip formatting or tests. Fix all failures before finishing.
