package tfrun

import "github.com/go-git/go-git/v5"

type GitFacade interface {
	azureCheckoutRef(targetdir, refName string) error
	azureClone(targetdir, url string, ref *string, authtype AuthType) error
	checkoutRef(repo *git.Repository, refName string) error
	clone(auth auth, url, targetdir string) (*git.Repository, error)
	moveDirContent(src string, dst string) error
	setLog(log *logwrap)
}
