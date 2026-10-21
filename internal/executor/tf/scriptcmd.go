package tf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
)

type ScriptParams struct {
	Name            string
	Script          string
	RunMode         string
	WorkDir         string
	TerraformBinDir string
	// RunJson goes to the script on stdin; a script that does not read it ignores it.
	RunJson  []byte
	ExtraEnv map[string]string
}

// ScriptResult contains the output produced by RunPreRunScript.
type ScriptResult struct {
	// UserMessage is the trimmed content of the file pointed to by $MESHSTACK_USER_MESSAGE.
	// Empty string when the script wrote nothing (or only whitespace) to that file.
	UserMessage string

	// SystemMessage is the combined stdout and stderr captured from the script process.
	SystemMessage string

	// Failure is the error of a script that ran and failed; RunScript returns an error only when the script could not run.
	Failure error

	// ExitCode is the process exit code; 0 on success.
	ExitCode int
}

// RunScript runs the script with bash -e -o pipefail, as GitHub Actions does, and the run mode as $1.
// Its output goes to the system log only; a script reaches the user through $MESHSTACK_USER_MESSAGE.
// The run JSON on stdin holds the decrypted sensitive inputs.
func RunScript(ctx context.Context, params ScriptParams) (ScriptResult, error) {
	scriptPath, err := writeScriptFile(params.WorkDir, params.Name, params.Script)
	if err != nil {
		return ScriptResult{}, err
	}

	userMsgPath, err := createUserMsgFile(params.WorkDir)
	if err != nil {
		return ScriptResult{}, err
	}
	defer func() {
		_ = os.Remove(userMsgPath)
	}()

	environmentVariables := buildScriptEnvironmentVariables(params.TerraformBinDir, userMsgPath, params.ExtraEnv)
	cmd := buildScriptCmd(ctx, params.WorkDir, scriptPath,
		environmentVariables,
		params.RunJson, // stdin for script
		// args for script
		params.RunMode,
	)

	outputBytes, runErr := cmd.CombinedOutput()
	exitCode := extractExitCode(runErr)
	userMsg := readUserMsgFile(userMsgPath)

	result := ScriptResult{
		UserMessage:   userMsg,
		SystemMessage: string(outputBytes),
		ExitCode:      exitCode,
	}
	if runErr != nil {
		result.Failure = fmt.Errorf("%s script failed with exit code %d: %w", params.Name, exitCode, runErr)
	}
	return result, nil
}

// writeScriptFile writes the script content to meshstack-{name}.sh in wd and returns its path.
// The file is not made executable; bash is invoked directly so no execute bit is needed.
// Windows line endings are turned into Unix line endings by replacing all CRLF with LF.
func writeScriptFile(wd, name, script string) (string, error) {
	scriptPath := path.Join(wd, fmt.Sprintf("meshstack-%s.sh", name))
	if err := os.WriteFile(scriptPath, []byte(strings.ReplaceAll(script, "\r\n", "\n")), 0o600); err != nil {
		return "", fmt.Errorf("failed to write %s script: %w", name, err)
	}
	return scriptPath, nil
}

// createUserMsgFile creates a temporary file in wd for capturing the user-facing
// message written by the script via $MESHSTACK_USER_MESSAGE.
func createUserMsgFile(wd string) (string, error) {
	f, err := os.CreateTemp(wd, "meshstack-user-message-*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create user message temp file: %w", err)
	}
	_ = f.Close()
	return f.Name(), nil
}

// buildScriptCmd constructs the bash exec.Cmd for the pre-run script, wiring the
// working directory, environment, stdin and args together.
func buildScriptCmd(ctx context.Context, wd, scriptPath string, environ []string, stdin []byte, scriptArgs ...string) *exec.Cmd {
	args := append([]string{"--noprofile", "--norc", "-e", "-o", "pipefail", scriptPath}, scriptArgs...)
	cmd := exec.CommandContext(ctx, "bash", args...) //nolint:gosec // the pre-run script of the building block definition is the payload.
	cmd.Dir = wd
	cmd.Env = environ
	if len(stdin) > 0 {
		// Go writes stdin asynchronously so there is no deadlock risk for scripts that discard stdin.
		cmd.Stdin = bytes.NewReader(stdin)
	}
	return cmd
}

func buildScriptEnvironmentVariables(terraformBinDir, userMsgPath string, extraEnv map[string]string) []string {
	merged := cleanSystemEnv()
	maps.Copy(merged, extraEnv)
	merged["MESHSTACK_USER_MESSAGE"] = userMsgPath

	return prependToPathEnvironmentVariable(environ(merged), terraformBinDir)
}

func prependToPathEnvironmentVariable(environ []string, paths ...string) []string {
	paths = slices.DeleteFunc(paths, func(path string) bool {
		return strings.TrimSpace(path) == ""
	})
	if len(paths) == 0 {
		return environ
	}
	pathSeparator := string(os.PathListSeparator)
	const (
		pathKeyPrefix = "PATH="
	)
	pathFound := false
	for i, envKeyValue := range environ {
		if after, ok := strings.CutPrefix(envKeyValue, pathKeyPrefix); ok {
			if existingPaths := after; existingPaths != "" {
				paths = append(paths, existingPaths)
			}
			environ[i] = pathKeyPrefix + strings.Join(paths, pathSeparator)
			pathFound = true
		}
	}
	if !pathFound {
		environ = append(environ, pathKeyPrefix+strings.Join(paths, pathSeparator))
	}
	return environ
}

// extractExitCode returns the process exit code from the error returned by cmd.Run / cmd.CombinedOutput.
// Returns 0 for nil (success), -1 when the error does not carry an exit code.
func extractExitCode(runErr error) int {
	if runErr == nil {
		return 0
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
		return exitErr.ExitCode()
	}

	return -1
}

// readUserMsgFile reads the user message file and returns its trimmed content.
// Returns an empty string when the file is empty, contains only whitespace, or cannot be read.
func readUserMsgFile(filePath string) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}
