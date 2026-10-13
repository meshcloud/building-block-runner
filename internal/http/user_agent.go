package http

import (
	"fmt"
	"strings"
)

type UserAgent struct {
	GitHubRepo string
	Version    string
}

func (u UserAgent) Validate() error {
	org, repo, ok := strings.Cut(u.GitHubRepo, "/")
	if !ok || org == "" || repo == "" {
		return fmt.Errorf("GitHub repo '%s' is not of <org>/<repo> format", u.GitHubRepo)
	}
	if u.Version == "" {
		return fmt.Errorf("no version given for GitHub repo '%s'", u.GitHubRepo)
	}
	return nil
}

func (u UserAgent) String() string {
	_, repo, _ := strings.Cut(u.GitHubRepo, "/")
	return repo + "/" + u.Version
}
