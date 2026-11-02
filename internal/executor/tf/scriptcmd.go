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
	// RunJson holds the decrypted sensitive inputs.
	RunJson  []byte
	ExtraEnv map[string]string
}

type ScriptResult struct {
	UserMessage   string
	SystemMessage string
	// Failure is the error of a script that ran and failed; RunScript returns an error only when the script could not run.
	Failure  error
	ExitCode int
}

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
	cmd := buildScriptCmd(ctx, params, scriptPath, environmentVariables)

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

// bash reads the file, so it needs no execute bit.
func writeScriptFile(wd, name, script string) (string, error) {
	scriptPath := path.Join(wd, fmt.Sprintf("meshstack-%s.sh", name))
	if err := os.WriteFile(scriptPath, []byte(strings.ReplaceAll(script, "\r\n", "\n")), 0o600); err != nil {
		return "", fmt.Errorf("failed to write %s script: %w", name, err)
	}
	return scriptPath, nil
}

func createUserMsgFile(wd string) (string, error) {
	f, err := os.CreateTemp(wd, "meshstack-user-message-*.txt")
	if err != nil {
		return "", fmt.Errorf("failed to create user message temp file: %w", err)
	}
	_ = f.Close()
	return f.Name(), nil
}

func buildScriptCmd(ctx context.Context, params ScriptParams, scriptPath string, environ []string) *exec.Cmd {
	// The same flags as GitHub Actions uses.
	args := []string{"--noprofile", "--norc", "-e", "-o", "pipefail", scriptPath, params.RunMode}
	cmd := exec.CommandContext(ctx, "bash", args...) //nolint:gosec // the pre-run script of the building block definition is the payload.
	cmd.Dir = params.WorkDir
	cmd.Env = environ
	if len(params.RunJson) > 0 {
		cmd.Stdin = bytes.NewReader(params.RunJson)
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

func extractExitCode(runErr error) int {
	if runErr == nil {
		return 0
	}

	if exitErr, ok := errors.AsType[*exec.ExitError](runErr); ok {
		return exitErr.ExitCode()
	}

	return -1
}

func readUserMsgFile(filePath string) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}
