package report

import "testing"

func TestExecutionStatus_String(t *testing.T) {
	cases := map[ExecutionStatus]string{
		PENDING:               "PENDING",
		IN_PROGRESS:           "IN_PROGRESS",
		SUCCEEDED:             "SUCCEEDED",
		FAILED:                "FAILED",
		ABORTED:               "ABORTED",
		ExecutionStatus(1234): "UNKNOWN",
	}

	for status, want := range cases {
		if got := status.String(); got != want {
			t.Errorf("ExecutionStatus(%d).String() = %q, want %q", status, got, want)
		}
	}
}

func TestExecutionStatus_IsTerminal(t *testing.T) {
	terminal := map[ExecutionStatus]bool{
		PENDING:     false,
		IN_PROGRESS: false,
		SUCCEEDED:   true,
		FAILED:      true,
		ABORTED:     true,
	}

	for status, want := range terminal {
		if got := status.IsTerminal(); got != want {
			t.Errorf("%s.IsTerminal() = %v, want %v", status, got, want)
		}
	}
}

func TestExecutionStatus_ZeroValueIsPending(t *testing.T) {
	var s ExecutionStatus
	if s != PENDING {
		t.Errorf("zero value = %v, want PENDING", s)
	}
}
