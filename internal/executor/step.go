package executor

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/meshcloud/meshstack-cli/client"
)

const abortedByEarlierStep = "Aborted due to failure in an earlier step"

// Step is one step that meshStack shows for the run. The mutex of its Reporter guards all steps.
type Step struct {
	r           *Reporter
	id          string
	displayName string

	status      Status
	userMessage strings.Builder
	log         strings.Builder
	partialLine []byte
	outputs     map[string]client.MeshRunnerRunOutput
}

// Run reports fn's error as the user message unless the step has one already, such as a pre-run script's.
func (s *Step) Run(fn func() error) error {
	s.r.mu.Lock()
	s.status = StatusInProgress
	s.r.mu.Unlock()

	err := fn()

	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	if err == nil {
		s.status = StatusSucceeded
		return nil
	}
	s.r.status = StatusFailed
	s.r.failure = err.Error()
	s.failLocked(err.Error())
	return err
}

// Write logs each line at debug only, as the step log holds the customer's tofu output.
func (s *Step) Write(p []byte) (int, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	s.log.Write(p)
	s.partialLine = append(s.partialLine, p...)
	lines, partial, found := bytes.CutLast(s.partialLine, []byte("\n"))
	if !found {
		return len(p), nil
	}
	for line := range bytes.SplitSeq(lines, []byte("\n")) {
		s.debug(s.r.logCtx, line)
	}
	s.partialLine = append(s.partialLine[:0], partial...)
	return len(p), nil
}

func (s *Step) Logf(format string, args ...any) {
	_, _ = s.Write([]byte(line(format, args...)))
}

func (s *Step) UserLogf(format string, args ...any) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	s.userMessage.WriteString(line(format, args...))
}

func (s *Step) SetOutputs(outputs map[string]client.MeshRunnerRunOutput) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	s.outputs = outputs
}

func line(format string, args ...any) string {
	text := fmt.Sprintf(format, args...)
	if strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

// failLocked fails this step and the ones after it; the caller holds the mutex.
func (s *Step) failLocked(userMessage string) {
	s.status = StatusFailed
	if s.userMessage.Len() == 0 && userMessage != "" {
		s.userMessage.WriteString(line("%s", userMessage))
	}
	later := s.r.steps[slices.Index(s.r.steps, s)+1:]
	for _, step := range later {
		if step.status == StatusPending {
			step.status = StatusFailed
			step.log.WriteString(abortedByEarlierStep)
		}
	}
}

func (s *Step) flushPartialLine(ctx context.Context) {
	if len(s.partialLine) > 0 {
		s.debug(ctx, s.partialLine)
		s.partialLine = s.partialLine[:0]
	}
}

func (s *Step) debug(ctx context.Context, line []byte) {
	slog.DebugContext(ctx, string(line), "step", s.id)
}

func (s *Step) update() client.MeshRunnerRunStepStatusUpdate {
	var userMessage, systemMessage *string
	if s.userMessage.Len() > 0 {
		userMessage = new(s.userMessage.String())
	}
	if s.log.Len() > 0 {
		systemMessage = new(s.log.String())
	}
	return client.MeshRunnerRunStepStatusUpdate{
		Id:            s.id,
		DisplayName:   s.displayName,
		Status:        new(string(s.status)),
		UserMessage:   userMessage,
		SystemMessage: systemMessage,
		Outputs:       s.outputs,
	}
}
