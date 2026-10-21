package tf

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meshcloud/meshstack-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makePreRunScriptCmd(t *testing.T, preRunScript *string, downloadTfBinaries bool) (*GenericCmd, func() string) {
	t.Helper()

	reporter, steps, readSystemLog := newTestReporter(t)

	// An old version tells it from a tofu on the PATH; tofudl cannot download 1.6.0 for linux/amd64.
	const tofuVersion = "1.7.0"
	tfRun := run{Json: []byte(`{"buildingBlockId":"test-bb","runId":"test-run"}`), behavior: behaviorApply}
	tfRun.Spec.Definition.Spec.Implementation.Terraform = &client.MeshRunnerTerraformImplementation{TerraformVersion: tofuVersion, PreRunScript: preRunScript}
	sut := &GenericCmd{
		ctx:       context.Background(),
		reporter:  reporter,
		steps:     steps,
		run:       tfRun,
		moduleDir: t.TempDir(),
		bin:       &Binaries{dir: "/tmp/terraform-bin"},
	}

	if downloadTfBinaries {
		var err error
		sut.bin, err = NewBinaries(t.TempDir(), time.Minute, io.Discard)
		require.NoError(t, err)
		_, err = sut.bin.install(t.Context(), tofuVersion)
		require.NoError(t, err)
	}

	return sut, readSystemLog
}

func Test_runPreRunScript_nilScript_returnsNilNoError(t *testing.T) {
	sut, _ := makePreRunScriptCmd(t, nil, false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	assert.Empty(t, userMsg)
}

func Test_runPreRunScript_emptyScript_returnsNilNoError(t *testing.T) {
	sut, _ := makePreRunScriptCmd(t, new(""), false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	assert.Empty(t, userMsg)
}

func Test_runPreRunScript_scriptWithWindowsLineEndings_returnsNilNoError(t *testing.T) {
	sut, _ := makePreRunScriptCmd(t, new("echo Hello\r\n\r\necho Windows\r\n"), false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	assert.Empty(t, userMsg)
}

func Test_runPreRunScript_userMessageFile_returnedAndSetAsUserMessage(t *testing.T) {
	script := "echo 'hello user' >> \"$MESHSTACK_USER_MESSAGE\""
	sut, _ := makePreRunScriptCmd(t, &script, false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotEmpty(t, userMsg)
	assert.Contains(t, userMsg, "hello user")
}

func Test_runPreRunScript_failedScript_userMessageFile_stillSetAsUserMessage(t *testing.T) {
	script := "echo 'partial output' >> \"$MESHSTACK_USER_MESSAGE\"\nexit 1"
	sut, _ := makePreRunScriptCmd(t, &script, false)

	userMsg, err := sut.runPreRunScript(nil)

	require.Error(t, err)
	require.NotEmpty(t, userMsg, "UserMessage must be set even when the script fails")
	assert.Contains(t, userMsg, "partial output")
}

func Test_runPreRunScript_failedScript_userMessageLoggedToSystemLog(t *testing.T) {
	script := "echo 'user-visible pre-run failure' >> \"$MESHSTACK_USER_MESSAGE\"\nexit 1"
	sut, readSystemLog := makePreRunScriptCmd(t, &script, false)

	_, _ = sut.runPreRunScript(nil)

	assert.Contains(t, readSystemLog(), "user-visible pre-run failure")
}

func Test_runPreRunScript_failedScript_stdoutGoesToSystemLog(t *testing.T) {
	script := "echo 'debug detail'\nexit 1"
	sut, readSystemLog := makePreRunScriptCmd(t, &script, false)

	_, _ = sut.runPreRunScript(nil)

	assert.Contains(t, readSystemLog(), "debug detail")
}

func Test_runPreRunScript_exitCodeAlwaysLoggedToSystemLog(t *testing.T) {
	successScript := "exit 0"
	failScript := "exit 42"

	sut0, readLog0 := makePreRunScriptCmd(t, &successScript, false)
	_, _ = sut0.runPreRunScript(nil)
	assert.Contains(t, readLog0(), "exited with code 0")

	sut42, readLog42 := makePreRunScriptCmd(t, &failScript, false)
	_, _ = sut42.runPreRunScript(nil)
	assert.Contains(t, readLog42(), "exited with code 42")
}

func Test_runPreRunScript_errorMessageContainsExitCode(t *testing.T) {
	script := "exit 7"
	sut, _ := makePreRunScriptCmd(t, &script, false)

	_, err := sut.runPreRunScript(nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "7")
}

func Test_runPreRunScript_stdoutAndStderr_bothGoToSystemLog(t *testing.T) {
	script := "echo 'this is stdout'\necho 'this is stderr' >&2"
	sut, readSystemLog := makePreRunScriptCmd(t, &script, false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	assert.Empty(t, userMsg)
	assert.Contains(t, readSystemLog(), "this is stdout\nthis is stderr")
}

func Test_runPreRunScript_userMessageFile_doesNotLeakIntoSystemLog(t *testing.T) {
	script := "echo 'user-only content' >> \"$MESHSTACK_USER_MESSAGE\""
	sut, readSystemLog := makePreRunScriptCmd(t, &script, false)

	_, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	assert.NotContains(t, readSystemLog(), "user-only content",
		"$MESHSTACK_USER_MESSAGE content must not appear in the system log")
}

func Test_runPreRunScript_bashInterpreterUsed(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	script := "echo \"interpreter: $BASH_VERSION\" >> \"$MESHSTACK_USER_MESSAGE\""
	sut, _ := makePreRunScriptCmd(t, &script, false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotEmpty(t, userMsg)
	assert.NotEqual(t, "interpreter: \n", userMsg, "expected bash to be used as interpreter")
}

func Test_runPreRunScript_runModePassedAsFirstArgument(t *testing.T) {
	script := "echo \"mode=$1\" >> \"$MESHSTACK_USER_MESSAGE\""
	sut, _ := makePreRunScriptCmd(t, &script, false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotEmpty(t, userMsg)
	assert.Contains(t, userMsg, "mode=APPLY")
}

func Test_runPreRunScript_runJsonPassedOnStdin(t *testing.T) {
	script := "cat - >> \"$MESHSTACK_USER_MESSAGE\""
	sut, _ := makePreRunScriptCmd(t, &script, false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotEmpty(t, userMsg)
	assert.Contains(t, userMsg, "buildingBlockId")
	assert.Contains(t, userMsg, "test-bb")
}

func Test_runPreRunScript_scriptThatIgnoresStdin_doesNotHang(t *testing.T) {
	script := "echo 'done' >> \"$MESHSTACK_USER_MESSAGE\""
	sut, _ := makePreRunScriptCmd(t, &script, false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotEmpty(t, userMsg)
	assert.Contains(t, userMsg, "done")
}

func Test_runPreRunScript_cwdIsWorkingDirectory(t *testing.T) {
	script := "pwd >> \"$MESHSTACK_USER_MESSAGE\""
	sut, _ := makePreRunScriptCmd(t, &script, false)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotEmpty(t, userMsg)
	// os.MkdirTemp may return a symlink on macOS (/var → /private/var); resolve both sides.
	wdResolved, err := filepath.EvalSymlinks(sut.moduleDir)
	require.NoError(t, err)
	assert.Containsf(t, []string{wdResolved, sut.moduleDir}, strings.TrimSpace(userMsg),
		"expected cwd %q or %q, got %q",
		sut.moduleDir, wdResolved, strings.TrimSpace(userMsg),
	)
}

func TestAccRunPreRunScript_tofuAndTerraformBinPresent(t *testing.T) {
	skipUnlessAcc(t)
	script := `tofu version >>"$MESHSTACK_USER_MESSAGE"`
	sut, _ := makePreRunScriptCmd(t, &script, true)

	userMsg, err := sut.runPreRunScript(nil)

	require.NoError(t, err)
	require.NotEmpty(t, userMsg)
	require.Contains(t, userMsg, "OpenTofu v1.7.0")
}
