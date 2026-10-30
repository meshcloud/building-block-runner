package executor

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"uuid"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type acceptingRunApi struct {
	client.MeshRunnerRunClient
}

func (acceptingRunApi) RegisterSource(context.Context, client.MeshRunnerRunSourceRegistration) error {
	return nil
}

func (acceptingRunApi) PatchStatus(context.Context, uuid.UUID, client.MeshRunnerRunStatusUpdate) (client.MeshRunnerRunStatusUpdateResult, error) {
	return client.MeshRunnerRunStatusUpdateResult{}, nil
}

func recordDebugLogs(t *testing.T) *strings.Builder {
	t.Helper()
	var logs strings.Builder
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	})))
	return &logs
}

func Test_StepLog_LogsEachLineAtDebugOnceItIsComplete(t *testing.T) {
	logs := recordDebugLogs(t)
	r := NewReporter(acceptingRunApi{}, runnerUuid)
	initStep, applyStep := r.Step("init", "Init"), r.Step("apply", "Apply")
	require.NoError(t, r.Register(t.Context()))

	require.NoError(t, initStep.Run(func() error {
		_, _ = initStep.Write([]byte("first li"))
		_, _ = initStep.Write([]byte("ne\nsecond\nthi"))
		assert.Equal(t, []string{`level=DEBUG msg="first line" step=init`, `level=DEBUG msg=second step=init`}, debugLines(logs))
		return nil
	}))
	require.NoError(t, applyStep.Run(func() error {
		applyStep.Logf("applying %d resources", 2)
		_, _ = applyStep.Write([]byte("no newline"))
		return nil
	}))
	status, err := r.Finish(t.Context())

	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, status)
	assert.Equal(t, []string{
		`level=DEBUG msg="first line" step=init`,
		`level=DEBUG msg=second step=init`,
		`level=DEBUG msg="applying 2 resources" step=apply`,
		`level=DEBUG msg=thi step=init`,
		`level=DEBUG msg="no newline" step=apply`,
	}, debugLines(logs))
}

func debugLines(logs *strings.Builder) []string {
	var lines []string
	for line := range strings.Lines(logs.String()) {
		if strings.HasPrefix(line, "level=DEBUG") {
			lines = append(lines, strings.TrimSuffix(line, "\n"))
		}
	}
	return lines
}

type recordingRunApi struct {
	acceptingRunApi

	last client.MeshRunnerRunStatusUpdate
}

func (a *recordingRunApi) PatchStatus(_ context.Context, _ uuid.UUID, update client.MeshRunnerRunStatusUpdate) (client.MeshRunnerRunStatusUpdateResult, error) {
	a.last = update
	return client.MeshRunnerRunStatusUpdateResult{}, nil
}

func Test_StepRun_FailsTheRunKeepingAnEarlierUserMessage(t *testing.T) {
	for name, test := range map[string]struct {
		userLine    string
		userMessage string
	}{
		"the error is the user message":          {userMessage: "tofu failed\n"},
		"a user message of the step stays alone": {userLine: "fix your input", userMessage: "fix your input\n"},
	} {
		t.Run(name, func(t *testing.T) {
			api := &recordingRunApi{}
			r := NewReporter(api, runnerUuid)
			first, failing := r.Step("first", "First"), r.Step("failing", "Failing")
			r.Step("later", "Later")
			require.NoError(t, r.Register(t.Context()))
			require.NoError(t, first.Run(func() error { return nil }))

			err := failing.Run(func() error {
				if test.userLine != "" {
					failing.UserLogf("%s", test.userLine)
				}
				return errors.New("tofu failed")
			})
			status, finishErr := r.Finish(t.Context())

			require.EqualError(t, err, "tofu failed")
			require.NoError(t, finishErr)
			assert.Equal(t, StatusFailed, status)
			steps := api.last.Steps
			assert.Equal(t, []string{"SUCCEEDED", "FAILED", "FAILED"}, []string{*steps[0].Status, *steps[1].Status, *steps[2].Status})
			assert.Equal(t, test.userMessage, *steps[1].UserMessage)
			assert.Equal(t, abortedByEarlierStep, *steps[2].SystemMessage)
		})
	}
}

func Test_Step_AddedAfterRegisterGoesOutWithTheNextUpdate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &recordingRunApi{}
		r := NewReporter(api, runnerUuid)
		trigger := r.Step("trigger", "Trigger")
		require.NoError(t, r.Register(t.Context()))
		stop := r.observe(t.Context(), func(error) {})
		defer stop()

		require.NoError(t, trigger.Run(func() error {
			r.Step("job-1", "GitHub Job: deploy").SetStatus(StatusInProgress)
			synctest.Sleep(statusUpdateInterval)
			return nil
		}))

		require.Len(t, api.last.Steps, 2)
		job := api.last.Steps[1]
		assert.Equal(t, "job-1", job.Id)
		assert.Equal(t, "GitHub Job: deploy", job.DisplayName)
		assert.Equal(t, "IN_PROGRESS", *job.Status)
	})
}

func Test_StepSetStatus_LeavesTheRunStatusAlone(t *testing.T) {
	api := &recordingRunApi{}
	r := NewReporter(api, runnerUuid)
	trigger := r.Step("trigger", "Trigger")
	require.NoError(t, r.Register(t.Context()))

	require.NoError(t, trigger.Run(func() error {
		r.Step("job-1", "Build").SetStatus(StatusSucceeded)
		r.Step("job-2", "Optional check").SetStatus(StatusFailed)
		return nil
	}))
	status, err := r.Finish(t.Context())

	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, status)
	assert.Equal(t, "SUCCEEDED", *api.last.Status)
	var statuses []string
	for _, step := range api.last.Steps {
		statuses = append(statuses, *step.Status)
	}
	assert.Equal(t, []string{"SUCCEEDED", "SUCCEEDED", "FAILED"}, statuses)
}

func Test_Finish_FailsTheFirstStepThatDidNotEnd(t *testing.T) {
	api := &recordingRunApi{}
	r := NewReporter(api, runnerUuid)
	r.Step("trigger", "Trigger").SetStatus(StatusSucceeded)
	r.Step("job-1", "Optional check").SetStatus(StatusFailed)
	r.Step("job-2", "Deploy").SetStatus(StatusInProgress)
	require.NoError(t, r.Register(t.Context()))

	status, err := r.Finish(t.Context())

	require.NoError(t, err)
	assert.Equal(t, StatusFailed, status)
	steps := api.last.Steps
	assert.Equal(t, []string{"SUCCEEDED", "FAILED", "FAILED"}, []string{*steps[0].Status, *steps[1].Status, *steps[2].Status})
	assert.Nil(t, steps[1].UserMessage)
	assert.Equal(t, "The run ended without a final status.\n", *steps[2].UserMessage)
}

type countingRunApi struct {
	acceptingRunApi

	mu      sync.Mutex
	patches int
}

func (a *countingRunApi) PatchStatus(context.Context, uuid.UUID, client.MeshRunnerRunStatusUpdate) (client.MeshRunnerRunStatusUpdateResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.patches++
	return client.MeshRunnerRunStatusUpdateResult{}, nil
}

func Test_Progress_SkipsAnUnchangedStatusUntilTheKeepalive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &countingRunApi{}
		r := NewReporter(api, runnerUuid)
		step := r.Step("apply", "Apply")
		require.NoError(t, r.Register(t.Context()))
		stop := r.observe(t.Context(), func(error) {})
		defer stop()
		patchesAfter := func(wait time.Duration) int {
			synctest.Sleep(wait)
			api.mu.Lock()
			defer api.mu.Unlock()
			return api.patches
		}

		assert.Equal(t, 1, patchesAfter(statusUpdateInterval), "the first update")
		assert.Equal(t, 1, patchesAfter(statusUpdateInterval), "nothing changed")
		step.Logf("applying")
		assert.Equal(t, 2, patchesAfter(statusUpdateInterval), "the log changed")
		assert.Equal(t, 2, patchesAfter(keepaliveInterval-statusUpdateInterval), "nothing changed within the keepalive")
		assert.Equal(t, 3, patchesAfter(statusUpdateInterval), "the keepalive")
	})
}
