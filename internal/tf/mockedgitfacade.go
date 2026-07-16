package tf

import (
	"reflect"

	"github.com/go-git/go-git/v5"
)

type MockedGitFacade struct {
	GitFacade
	lastInvocations map[string][]any
}

type Any struct{}

func (g *MockedGitFacade) init() {
	g.lastInvocations = make(map[string][]any)
}

func (g *MockedGitFacade) called(funcname string, args ...any) bool {
	callArgs, exists := g.lastInvocations[funcname]
	if !exists {
		return false
	}
	if len(callArgs) != len(args) {
		return false
	}

	anyVal := Any{}
	for idx, a := range callArgs {
		if args[idx] == nil && reflect.ValueOf(a).IsNil() { // compare nils of different types
			continue
		}
		if args[idx] != anyVal && a != args[idx] {
			return false
		}
	}

	return true
}

func (g *MockedGitFacade) neverCalled(funcname string) bool {
	_, exists := g.lastInvocations[funcname]
	return !exists
}

func (g *MockedGitFacade) azureCheckoutRef(targetdir, ref string) error {
	g.lastInvocations["azureCheckoutRef"] = []any{targetdir, ref}
	return nil
}

func (g *MockedGitFacade) azureClone(targetdir, url string, ref *string, authtype AuthType) error {
	g.lastInvocations["azureClone"] = []any{targetdir, url, ref, authtype}
	return nil
}

func (g *MockedGitFacade) checkoutRef(repo *git.Repository, ref string) error {
	g.lastInvocations["checkoutRef"] = []any{repo, ref}
	return nil
}

func (g *MockedGitFacade) clone(auth auth, url, targetdir string) (*git.Repository, error) {
	g.lastInvocations["clone"] = []any{auth, url, targetdir}
	return nil, nil
}

func (g *MockedGitFacade) moveDirContent(src string, dst string) error {
	g.lastInvocations["moveDirContent"] = []any{src, dst}
	return g.GitFacade.moveDirContent(src, dst)
}

func (g *MockedGitFacade) setLog(log *logwrap) {
	g.lastInvocations["setLog"] = []any{log}
}
