package tf

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"slices"
	"strings"
)

// ScriptParams holds all inputs required to execute a hook script.
type ScriptParams struct {
	// Name identifies the hook (e.g. "pre-run", "post-run"). It is used to name
	// the script file written to WorkDir (meshstack-{name}.sh) and appears in
	// error messages, making it easy to distinguish hook failures in logs.
	Name string

	// Script is the bash script content. No shebang is required.
	Script string

	// RunMode is the building block run mode (e.g. "APPLY", "DETECT", "DESTROY").
	// It is passed as the first positional argument ($1) to the script.
	RunMode string

	// WorkDir is used as the working directory for the script.
	// The script file and the user message temp file are created here.
	WorkDir string

	// TerraformBinDir contains the 'terraform' binary which is downloaded by the TF runner.
	// If Terraform version is beyond 1.5.5, OpenTofu is used transparently and a tofu symlink is provided for convenience.
	TerraformBinDir string

	// RunJsonBase64 is the base64-encoded building block run JSON, supplied to the script on stdin.
	// Scripts that do not read stdin discard it without error.
	RunJsonBase64 string

	// ExtraEnv contains additional environment variables to layer on top of the minimal clean system
	// environment (HOME, PATH, TMPDIR, etc.) when executing the script. Typically this is the map
	// returned by GenericTfCmd.buildTfEnv so that input variables marked as env-vars and
	// GIT_SSH_COMMAND reach the script. Passing nil means only the system vars are available.
	ExtraEnv map[string]string
}

// ScriptResult contains the output produced by RunPreRunScript.
type ScriptResult struct {
	// UserMessage is the trimmed content of the file pointed to by $MESHSTACK_USER_MESSAGE.
	// Empty string when the script wrote nothing (or only whitespace) to that file.
	UserMessage string

	// SystemMessage is the combined stdout and stderr captured from the script process.
	SystemMessage string

	// ExitCode is the process exit code; 0 on success.
	ExitCode int
}

// RunScript writes and executes a hook script described by params.
// Both stdout and stderr are captured in ScriptResult.SystemMessage so that operator-level
// detail (including errors from sub-commands) never surfaces directly to end-users.
// Platform engineers who want to communicate with users must write explicitly to the
// file pointed to by the MESHSTACK_USER_MESSAGE environment variable, which is set
// by the runner before executing the script. Its trimmed content is returned in
// ScriptResult.UserMessage. The building block run JSON is available on stdin and
// can be parsed with jq (pre-installed on runner images) to read run metadata:
//
//	WORKSPACE=$(jq -r '.spec.buildingBlock.spec.workspaceIdentifier')
//	echo "Provisioning finished for workspace $WORKSPACE" >> "$MESHSTACK_USER_MESSAGE"
//
// The script is executed using 'bash --noprofile --norc -e -o pipefail', following
// the same approach as GitHub Actions
// (https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#defaultsrun).
// No shebang line is required. The run mode (APPLY, DETECT, DESTROY) is provided
// as the first positional argument ($1).
//
// The script has a PATH environment set up such that tofu (and legacy terraform) commands
// can be called within the script.
//
// SECURITY NOTE: sensitive building block inputs are available in decrypted form in
// the run JSON passed on stdin. Platform engineers must be careful not to log or
// expose these values (e.g. avoid logging the full JSON or printing sensitive input
// values to stdout).
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

	runJSON, err := decodeRunJSON(params.RunJsonBase64)
	if err != nil {
		return ScriptResult{}, err
	}

	environmentVariables := buildScriptEnvironmentVariables(params.TerraformBinDir, userMsgPath, params.ExtraEnv)
	cmd := buildScriptCmd(ctx, params.WorkDir, scriptPath,
		environmentVariables,
		runJSON, // stdin for script
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
		return result, fmt.Errorf("%s script failed with exit code %d: %w", params.Name, exitCode, runErr)
	}

	return result, nil
}

// writeScriptFile writes the script content to meshstack-{name}.sh in wd and returns its path.
// The file is not made executable; bash is invoked directly so no execute bit is needed.
// Windows line endings are turned into Unix line endings by replacing all CRLF with LF.
func writeScriptFile(wd, name, script string) (string, error) {
	scriptPath := path.Join(wd, fmt.Sprintf("meshstack-%s.sh", name))
	if err := os.WriteFile(scriptPath, []byte(strings.ReplaceAll(script, "\r\n", "\n")), 0600); err != nil {
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

// decodeRunJSON decodes the base64-encoded building block run JSON for use as stdin.
// Returns an error if the string is non-empty but cannot be decoded.
func decodeRunJSON(base64Json string) ([]byte, error) {
	if base64Json == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(base64Json)
	if err != nil {
		return nil, fmt.Errorf("could not decode run JSON: %w", err)
	}

	return decoded, nil
}

// buildScriptCmd constructs the bash exec.Cmd for the pre-run script, wiring the
// working directory, environment, stdin and args together.
func buildScriptCmd(ctx context.Context, wd, scriptPath string, environ []string, stdin []byte, scriptArgs ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "bash", append([]string{"--noprofile", "--norc", "-e", "-o", "pipefail", scriptPath}, scriptArgs...)...)
	cmd.Dir = wd
	cmd.Env = environ
	if len(stdin) > 0 {
		// Go writes stdin asynchronously so there is no deadlock risk for scripts that discard stdin.
		cmd.Stdin = bytes.NewReader(stdin)
	}
	return cmd
}

func buildScriptEnvironmentVariables(terraformBinDir, userMsgPath string, extraEnv map[string]string) []string {
	// Start from the minimal clean system environment, then layer on any explicitly
	// configured variables (e.g. input vars marked as env, GIT_SSH_COMMAND).
	merged := cleanSystemEnv()
	for k, v := range extraEnv {
		merged[k] = v
	}
	merged["MESHSTACK_USER_MESSAGE"] = userMsgPath

	environ := make([]string, 0, len(merged))
	for k, v := range merged {
		environ = append(environ, fmt.Sprintf("%s=%s", k, v))
	}
	return prependToPathEnvironmentVariable(environ, terraformBinDir)
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
		if strings.HasPrefix(envKeyValue, pathKeyPrefix) {
			if existingPaths := strings.TrimPrefix(envKeyValue, pathKeyPrefix); existingPaths != "" {
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

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
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
