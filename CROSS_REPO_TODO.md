# Cross-repo hand-off notes

Notes accumulated during the single-Go-binary refactor that require action or awareness
in another repo. Not actioned here — tracked for a human to follow up.

## Phase 2b (tf bug-fix pass, PLAN_DETAIL_02 §7)

- **B2 fix — workspace-delete naming (meshfed-release, `local-dev-stack`):**
  `tfrun`'s `selectWorkspace`/`deleteWorkspaceIfNeeded` previously deleted a workspace name that
  never matched an actual tofu workspace on disk (a bug), so DESTROY runs silently left the real
  workspace behind. As of this fix, DESTROY now deletes the real, matched workspace name. This is
  a behavior change for any long-lived local-dev-stack state in meshfed-release: previously
  "destroyed" building blocks may have left orphaned tofu workspaces around, and those will now
  actually be removed on the next DESTROY. Flag this to meshfed-release maintainers; no code
  changes needed there, just an awareness note (and possibly a one-time cleanup of already-orphaned
  workspaces in shared local-dev environments).

- **B5 fix — sensitive non-string-like inputs are now decrypted (customer-visible):**
  `Variable.decryptIfSensitive` previously decrypted only CODE/STRING/FILE sensitive inputs; any
  other sensitive type (BOOLEAN, INTEGER, SINGLE_SELECT, MULTI_SELECT, LIST) silently passed its
  ciphertext through unchanged into the generated tfvars/env. It now decrypts every sensitive
  input regardless of type. Any building block that was (perhaps unknowingly) relying on the old
  passthrough-ciphertext behavior for a sensitive non-string-like input will see a different,
  correct value after this fix. Worth a release-notes call-out for customers.

- **B11 fix — single-run exit code (run-controller / k8s Job semantics):**
  The tf-block-runner's `EXECUTION_MODE=single-run` binary now exits non-zero for failures
  *before* the run's first potentially state-mutating step (workdir setup, run-JSON parse,
  registration) — previously it always exited 0. This is deliberately scoped narrower than "any
  failure exits non-zero" because the controller's k8s Job template uses
  `BackoffLimit: 1` + `RestartPolicy: Never` (`run-controller/controller/kubernetes.go`): a blanket
  non-zero exit on any failure (including a failed tofu apply) would make k8s automatically
  re-run a failed terraform run once — an unwanted second, automatic APPLY/DESTROY. No action
  needed in run-controller today (the Job template's `BackoffLimit`/`RestartPolicy` already match
  the assumption this fix relies on), but flag this coupling if that Job template ever changes.
