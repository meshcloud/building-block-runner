package config

import (
	"fmt"
	"slices"
	"strings"
)

type RunnerType string

const (
	RunnerTypeAll         RunnerType = "*"
	RunnerTypeTf          RunnerType = "tf"
	RunnerTypeManual      RunnerType = "manual"
	RunnerTypeGitHub      RunnerType = "github"
	RunnerTypeGitLab      RunnerType = "gitlab"
	RunnerTypeAzureDevOps RunnerType = "azureDevOps"
)

var allRunnerTypes = []RunnerType{RunnerTypeTf, RunnerTypeManual, RunnerTypeGitHub, RunnerTypeGitLab, RunnerTypeAzureDevOps}

var apiRunnerTypes = map[RunnerType]string{
	RunnerTypeAll:         "ALL",
	RunnerTypeTf:          "TERRAFORM",
	RunnerTypeManual:      "MANUAL",
	RunnerTypeGitHub:      "GITHUB_WORKFLOW",
	RunnerTypeGitLab:      "GITLAB_PIPELINE",
	RunnerTypeAzureDevOps: "AZURE_DEVOPS_PIPELINE",
}

// ParseRunnerType accepts the config name and the name in the meshStack API, in any case.
func ParseRunnerType(name string) (RunnerType, error) {
	for runnerType, apiName := range apiRunnerTypes {
		if strings.EqualFold(name, string(runnerType)) || strings.EqualFold(name, apiName) {
			return runnerType, nil
		}
	}
	return "", fmt.Errorf("runner type %s is unknown", name)
}

func (t RunnerType) ForApi() string {
	return apiRunnerTypes[t]
}

func (t *RunnerType) UnmarshalText(text []byte) (err error) {
	*t, err = ParseRunnerType(string(text))
	return
}

type RunnerTypes []RunnerType

// Expand replaces RunnerTypeAll with all runner types.
func (ts RunnerTypes) Expand() RunnerTypes {
	if slices.Contains(ts, RunnerTypeAll) {
		return slices.Clone(allRunnerTypes)
	}
	return ts
}

func (ts RunnerTypes) Contains(runnerType RunnerType) bool {
	return slices.Contains(ts.Expand(), runnerType)
}
