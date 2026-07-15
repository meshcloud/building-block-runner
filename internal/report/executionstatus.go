package report

// ExecutionStatus is the lifecycle state of a run or a single step within it. The zero value
// is PENDING (enums get a defined zero value) so an uninitialized ExecutionStatus never
// reads as a terminal or in-progress state by accident.
type ExecutionStatus int

const (
	PENDING = ExecutionStatus(iota)
	IN_PROGRESS
	SUCCEEDED
	FAILED
	// ABORTED is reported when an in-flight run is cancelled on shutdown so the coordinator
	// never observes a stale IN_PROGRESS that only clears after its own long timeout
	// (added for graceful shutdown). The runner-facing status endpoint accepts the
	// transition IN_PROGRESS->ABORTED and persists it as terminal; an already-aborted run
	// answers 409 {runAborted:true}, which callers treat as success, not as an error.
	ABORTED
)

// String implements fmt.Stringer. Unlike the tfrun-local predecessor this replaces, an
// unmapped value returns "UNKNOWN" rather than panicking: this type now crosses package
// boundaries into every runner's reporting path, so a stringer that can crash the process on a
// stray value is the wrong failure mode here (the same move already made for Behavior).
func (s ExecutionStatus) String() string {
	switch s {
	case PENDING:
		return "PENDING"
	case IN_PROGRESS:
		return "IN_PROGRESS"
	case SUCCEEDED:
		return "SUCCEEDED"
	case FAILED:
		return "FAILED"
	case ABORTED:
		return "ABORTED"
	default:
		return "UNKNOWN"
	}
}

// IsTerminal reports whether no further transitions are expected for this status.
func (s ExecutionStatus) IsTerminal() bool {
	return s == SUCCEEDED || s == FAILED || s == ABORTED
}
