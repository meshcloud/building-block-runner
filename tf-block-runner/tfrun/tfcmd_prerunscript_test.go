package tfrun

import (
	"context"
	"encoding/base64"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makePreRunScriptTfCmd extends the basic test helper with the fields required by runPreRunScript:
// a real context with TfBinaries downloaded (if flag is set), a run mode, and a logwrap that writes system messages to a readable temp file.
// Returns the command and a helper that reads all lines written to the system message (update) log.
func makePreRunScriptTfCmd(t *testing.T, preRunScript *string, runMode string, downloadTfBinaries bool) (*GenericTfCmd, func() string) {
	t.Helper()

	wd := t.TempDir()

	// Use a temp file so we can assert on what was written to the system message
	// (operator-visible update log). This is distinct from MESHSTACK_USER_MESSAGE,
	// which is a separate temp file created by runPreRunScript() for the user-facing message.
	systemLogFile, err := os.CreateTemp(wd, "update-log-*.txt")
	require.NoError(t, err)
	systemLogFileName := systemLogFile.Name()
	_ = systemLogFile.Close()

	lw := NewLogWrap(log.New(io.Discard, "[test] ", log.LstdFlags), systemLogFileName)

	// pick outdated tofu version intentionally as running tests locally might have newer Tofu version in PATH system-installed
	// Note: Version 1.6.0 does not work on linux/amd64 because of 'tofudl' bug:
	// "Unsupported platform (linux) or architecture (amd64) for OpenTofu version 1.6.0."
	const tofuVersion = "1.7.0"
	sut := &GenericTfCmd{
		ctx: context.Background(),
		params: &TfCmdParams{
			vars:         make(map[string]*Variable),
			preRunScript: preRunScript,
			runMode:      runMode,
			tfVersion:    tofuVersion,
		},
		runContextInfo: &RunContextInfo{
			workingDirectory: wd,
			logwrap:          lw,
			// Valid base64 encoding of {"buildingBlockId":"test-bb","runId":"test-run"}
			runJsonBase64: base64.StdEncoding.EncodeToString([]byte(`{"buildingBlockId":"test-bb","runId":"test-run"}`)),
			bbId:          "test-bb",
			runId:         "test-run",
			runStatus: &RunStatus{
				Steps: []*StepStatus{
					{Name: "pre-run"},
				},
				CurrentStepIndex: 0,
			},
		},
		bin: &TfBinaries{
			dir: "/tmp/terraform-bin",
		},
	}

	if downloadTfBinaries {
		sut.bin, err = NewTfBin(t.TempDir(), io.Discard)
		require.NoError(t, err)
		// NewTfBin can't be used easily as tfexec is not disentangled from TfBinaries, which is sad and needs refactoring!
		versionInstallPath := filepath.Join(sut.bin.dir, tofuVersion)
		require.NoError(t, os.MkdirAll(versionInstallPath, 0755))
		require.NoError(t, sut.bin.installTofuBinaries(tofuVersion, versionInstallPath))
	}

	readSystemLog := func() string {
		data, _ := os.ReadFile(systemLogFileName)
		return string(data)
	}

	return sut, readSystemLog
}

// strPtr returns a pointer to s. This is the idiomatic Go workaround for taking
// the address of a string literal, which the language does not permit directly.
func strPtr(s string) *string { return &s }

// ---------------------------------------------------------------------------
// Unit tests – step update logic
// ---------------------------------------------------------------------------

func Test_runPreRunScript_nilScript_returnsNilNoError(t *testing.T) {
	sut, _ := makePreRunScriptTfCmd(t, nil, "APPLY", false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	assert.Nil(t, userMsg)
}

func Test_runPreRunScript_emptyScript_returnsNilNoError(t *testing.T) {
	sut, _ := makePreRunScriptTfCmd(t, strPtr(""), "APPLY", false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	assert.Nil(t, userMsg)
}

func Test_runPreRunScript_scriptWithWindowsLineEndings_returnsNilNoError(t *testing.T) {
	sut, _ := makePreRunScriptTfCmd(t, strPtr("echo Hello\r\n\r\necho Windows\r\n"), "APPLY", false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	assert.Nil(t, userMsg)
}

func Test_runPreRunScript_userMessageFile_returnedAndSetAsUserMessage(t *testing.T) {
	// Platform engineers write user-facing messages via $MESHSTACK_USER_MESSAGE.
	script := "echo 'hello user' >> \"$MESHSTACK_USER_MESSAGE\""
	sut, _ := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotNil(t, userMsg)
	assert.Contains(t, *userMsg, "hello user")
}

func Test_runPreRunScript_failedScript_userMessageFile_stillSetAsUserMessage(t *testing.T) {
	// Script writes a user message then exits non-zero; the user message must still be preserved.
	script := "echo 'partial output' >> \"$MESHSTACK_USER_MESSAGE\"\nexit 1"
	sut, _ := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	userMsg, err := sut.runPreRunScript(nil)

	// UserMessage on the step must still carry the content even when the script fails.
	require.Error(t, err)
	require.NotNil(t, userMsg, "UserMessage must be set even when the script fails")
	assert.Contains(t, *userMsg, "partial output")
}

func Test_runPreRunScript_failedScript_userMessageLoggedToSystemLog(t *testing.T) {
	// When a script fails and has written to $MESHSTACK_USER_MESSAGE, the user message
	// must appear in the system (operator/update) log so it is visible in the run logs.
	script := "echo 'user-visible pre-run failure' >> \"$MESHSTACK_USER_MESSAGE\"\nexit 1"
	sut, readSystemLog := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	_, _ = sut.runPreRunScript(nil)

	assert.Contains(t, readSystemLog(), "user-visible pre-run failure")
}

func Test_runPreRunScript_failedScript_userMessageBecomesStepUserMessage(t *testing.T) {
	// When the script fails and has written to $MESHSTACK_USER_MESSAGE, the caller should
	// propagate that message via failWithUserMsg so the step's UserMessage is the
	// platform-engineer-provided text, not the generic error string.
	script := "echo 'user-visible pre-run failure' >> \"$MESHSTACK_USER_MESSAGE\"\nexit 1"
	sut, _ := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	userMsg, err := sut.runPreRunScript(nil)

	require.Error(t, err)
	require.NotNil(t, userMsg)
	sut.failWithUserMsg(err, userMsg)

	stepMsg := sut.runContextInfo.runStatus.currentStepStatus().UserMessage
	require.NotNil(t, stepMsg)
	assert.Contains(t, *stepMsg, "user-visible pre-run failure")
	assert.NotContains(t, *stepMsg, "exit status")
}

func Test_runPreRunScript_failedScript_stdoutGoesToSystemLog(t *testing.T) {
	script := "echo 'debug detail'\nexit 1"
	sut, readSystemLog := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	_, _ = sut.runPreRunScript(nil)

	assert.Contains(t, readSystemLog(), "debug detail")
}

func Test_runPreRunScript_exitCodeAlwaysLoggedToSystemLog(t *testing.T) {
	successScript := "exit 0"
	failScript := "exit 42"

	sut0, readLog0 := makePreRunScriptTfCmd(t, &successScript, "APPLY", false)
	_, _ = sut0.runPreRunScript(nil)
	assert.Contains(t, readLog0(), "exited with code 0")

	sut42, readLog42 := makePreRunScriptTfCmd(t, &failScript, "APPLY", false)
	_, _ = sut42.runPreRunScript(nil)
	assert.Contains(t, readLog42(), "exited with code 42")
}

func Test_runPreRunScript_errorMessageContainsExitCode(t *testing.T) {
	script := "exit 7"
	sut, _ := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	_, err := sut.runPreRunScript(nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "7")
}

// ---------------------------------------------------------------------------
// System log routing tests
//
// These tests verify that BOTH stdout and stderr from the script go exclusively
// to the system (operator-visible) log, never to UserMessage. UserMessage is
// only populated by content written to $MESHSTACK_USER_MESSAGE.
//
// The canonical scenario:
//
//	echo "operator debug output"              # → system log
//	echo "user-facing message" >> "$MESHSTACK_USER_MESSAGE"  # → UserMessage
//
// ---------------------------------------------------------------------------

func Test_runPreRunScript_stdoutAndStderr_bothGoToSystemLog(t *testing.T) {
	// When a script writes to both stdout and stderr, neither must appear in UserMessage.
	script := "echo 'this is stdout'\necho 'this is stderr' >&2"
	sut, readSystemLog := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	_, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	// Both streams must appear in the system log, in the correct order.
	assert.Contains(t, readSystemLog(), "this is stdout\nthis is stderr")
	// Neither must appear in UserMessage.
	assert.Nil(t, sut.runContextInfo.runStatus.currentStepStatus().UserMessage,
		"UserMessage must remain nil when nothing is written to $MESHSTACK_USER_MESSAGE")
}

func Test_runPreRunScript_userMessageFile_doesNotLeakIntoSystemLog(t *testing.T) {
	// Content written to $MESHSTACK_USER_MESSAGE must NOT appear in the system log.
	script := "echo 'user-only content' >> \"$MESHSTACK_USER_MESSAGE\""
	sut, readSystemLog := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	_, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	assert.NotContains(t, readSystemLog(), "user-only content",
		"$MESHSTACK_USER_MESSAGE content must not appear in the system log")
}

// ---------------------------------------------------------------------------
// E2E tests – real scripts, arguments, and stdin
// ---------------------------------------------------------------------------

func Test_runPreRunScript_bashInterpreterUsed(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	// BASH_VERSION is only set in bash; this verifies the script runs under bash.
	script := "echo \"interpreter: $BASH_VERSION\" >> \"$MESHSTACK_USER_MESSAGE\""
	sut, _ := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotNil(t, userMsg)
	assert.NotEqual(t, "interpreter: \n", *userMsg, "expected bash to be used as interpreter")
}

func Test_runPreRunScript_runModePassedAsFirstArgument(t *testing.T) {
	// The script writes $1 to the user message file so we can assert the run mode arrived.
	script := "echo \"mode=$1\" >> \"$MESHSTACK_USER_MESSAGE\""
	sut, _ := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotNil(t, userMsg)
	assert.Contains(t, *userMsg, "mode=APPLY")
}

func Test_runPreRunScript_runJsonPassedOnStdin(t *testing.T) {
	// Script reads all of stdin and writes it to the user message file.
	script := "cat - >> \"$MESHSTACK_USER_MESSAGE\""
	sut, _ := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotNil(t, userMsg)
	// The JSON supplied as stdin must appear in the user message file.
	assert.Contains(t, *userMsg, "buildingBlockId")
	assert.Contains(t, *userMsg, "test-bb")
}

func Test_runPreRunScript_scriptThatIgnoresStdin_doesNotHang(t *testing.T) {
	// This is the common case: a script that does not read stdin at all.
	script := "echo 'done' >> \"$MESHSTACK_USER_MESSAGE\""
	sut, _ := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotNil(t, userMsg)
	assert.Contains(t, *userMsg, "done")
}

func Test_runPreRunScript_cwdIsWorkingDirectory(t *testing.T) {
	// Script uses pwd to write the current directory to the user message file.
	script := "pwd >> \"$MESHSTACK_USER_MESSAGE\""
	sut, _ := makePreRunScriptTfCmd(t, &script, "APPLY", false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotNil(t, userMsg)
	// os.MkdirTemp may return a symlink on macOS (/var → /private/var); resolve both sides.
	wdResolved, err := filepath.EvalSymlinks(sut.runContextInfo.workingDirectory)
	require.NoError(t, err)
	assert.Containsf(t, []string{wdResolved, sut.runContextInfo.workingDirectory}, strings.TrimSpace(*userMsg),
		"expected cwd %q or %q, got %q",
		sut.runContextInfo.workingDirectory, wdResolved, strings.TrimSpace(*userMsg),
	)
}

func Test_runPreRunScript_tofuAndTerraformBinPresent(t *testing.T) {
	script := `tofu version >>"$MESHSTACK_USER_MESSAGE"`
	sut, _ := makePreRunScriptTfCmd(t, &script, "APPLY", true)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotNil(t, userMsg)
	require.Contains(t, *userMsg, "OpenTofu v1.7.0")
}
