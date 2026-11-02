package executor

type Status string

const (
	StatusPending    Status = "PENDING"
	StatusInProgress Status = "IN_PROGRESS"
	StatusSucceeded  Status = "SUCCEEDED"
	StatusFailed     Status = "FAILED"
	StatusAborted    Status = "ABORTED"
)
