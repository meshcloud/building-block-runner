package tfrun

type ExecutionStatus int

const (
	PENDING = ExecutionStatus(iota)
	IN_PROGRESS
	SUCCEEDED
	FAILED
)

func (status ExecutionStatus) isTerminalState() bool {
	return status == SUCCEEDED || status == FAILED
}

func (status ExecutionStatus) str() string {
	switch status {
	case PENDING:
		return "PENDING"
	case IN_PROGRESS:
		return "IN_PROGRESS"
	case SUCCEEDED:
		return "SUCCEEDED"
	case FAILED:
		return "FAILED"
	default:
		panic("Unmapped ExecutionStatus")
	}
}
