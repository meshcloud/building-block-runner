package tf

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
)

const (
	AZURE_DEVOPS_DOMAIN = "dev.azure.com"
	TEMP_CLONE_DIR_PATH = "tmp-clone-meshstack"

	MSG_CLONE_SUCCESS             = "Successfully cloned sources from your repository."
	MSG_FILES_AQUISITTION_SUCCESS = "Successfully copied files from your repository to the working directory."
)

type GitSource struct {
	url       string
	path      *string
	refName   *string
	auth      auth
	log       *logwrap
	gitFacade GitFacade
}

func (g *GitSource) setLog(log *logwrap) {
	g.log = log
	g.gitFacade.setLog(log)
}

func (g *GitSource) CopyToTargetDir(dir string) error {
	securityStr := ""
	if AppConfig.SkipHostKeyValidation {
		securityStr = "(insecure)"
	}

	fromStr := g.url
	if g.path != nil {
		fromStr += "/" + strings.TrimLeft(*g.path, "/")
	}

	msg := fmt.Sprintf("Attempt to copy sources from %s %s", fromStr, securityStr)
	g.log.PrintlnToLocalAndUpdateLogs(msg)

	// auth might need extra files, e.g. a key file or known_hosts
	// therefor we prepare those files before we do anything
	if err := g.auth.prepare(dir, g.log); err != nil {
		return err
	}

	// we need an extra directory to clone into, because we cannot clone into a non-empty dir
	// after everything is done, we remove the the dir again to not interfere with
	// source code dir content
	tmpDir := path.Join(dir, TEMP_CLONE_DIR_PATH)
	if err := os.Mkdir(tmpDir, 0700); err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	g.log.PrintlnToLocalLogs(fmt.Sprintf("cloning into temporary dir %s", tmpDir))

	// now we clone into the temporary directory
	// f*ck Azure
	if strings.Contains(g.url, AZURE_DEVOPS_DOMAIN) {
		g.log.PrintlnToLocalLogs("using azure devops workaround")
		err := g.gitFacade.azureClone(dir, g.url, g.refName, g.auth.name())
		if err != nil {
			return err
		}
		if g.refName != nil {
			if err := g.gitFacade.azureCheckoutRef(tmpDir, *g.refName); err != nil {
				return err
			}
		}

	} else {
		repo, err := g.gitFacade.clone(g.auth, g.url, tmpDir)
		if err != nil {
			return err
		}
		if g.refName != nil {
			err := g.gitFacade.checkoutRef(repo, *g.refName)

			if err != nil {
				// we observed some strange issues with git-go that prevented a successful switch of the
				// worktree contains unstaged changes
				if err.Error() == "worktree contains unstaged changes" {
					g.logDirectoryContentsForWorktreeUnstagedChangedError(tmpDir)
				}

				return err
			}
		}
	}

	// cloning is done, so auth is not needed anymore
	// we clean up the potentially needed auth files before we cloning into the wd
	// to avoid naming clashes
	if err := g.auth.done(); err != nil {
		return fmt.Errorf("cleaning up auth files: %w", err)
	}
	g.log.PrintlnToLocalAndUpdateLogs(MSG_CLONE_SUCCESS)

	// sources are now either directly in the temporary directory or in a subpath
	// depending on configuration
	sourceDir := tmpDir
	if g.path != nil {
		sourceDir = path.Join(sourceDir, *g.path)
	}

	if _, err := os.Stat(sourceDir); err != nil {
		// this means the specified path does not exist. Log sourceDir (the resolved path), not
		// *g.path (B9 fix): g.path is nil whenever no subpath was configured, and dereferencing it
		// unconditionally here nil-derefs in that case even though sourceDir is always valid.
		g.log.PrintlnToLocalLogs(fmt.Sprintf("path '%s' does not exist, cannot copy sources", sourceDir))
		_, _ = g.log.PrintlnToUpdateLogs(fmt.Sprintf("The specified path '%s' does not exist, please check repository configuration.", sourceDir))
		return errors.New("specified path does not exist")
	}

	// now we copy only the required sources to our target working directory
	g.log.PrintlnToLocalLogs(fmt.Sprintf("moving source files from temporary dir %s to %s", sourceDir, dir))
	err := g.gitFacade.moveDirContent(sourceDir, dir)
	if err == nil {
		g.log.PrintlnToLocalAndUpdateLogs(MSG_FILES_AQUISITTION_SUCCESS)
	}

	return err
}

func (g *GitSource) logDirectoryContentsForWorktreeUnstagedChangedError(dir string) {
	g.log.PrintlnToLocalLogs("Detected 'worktree contains unstaged changed error', printing temp dir content:")

	// scan the temp working directory so we can see which files are there causing this issue.
	entries, dirErr := os.ReadDir(dir)
	if dirErr == nil {
		// in case this errors out we just skip it.
		for _, entry := range entries {
			info, err := entry.Info() // Get additional file information
			if err != nil {
				continue
			}
			if entry.IsDir() {
				g.log.PrintlnToLocalLogs(fmt.Sprintf(" - %s (d)", entry.Name()))
			} else if info.Mode()&os.ModeSymlink != 0 {
				g.log.PrintlnToLocalLogs(fmt.Sprintf(" - %s (l)", entry.Name()))
			} else {
				g.log.PrintlnToLocalLogs(fmt.Sprintf(" - %s (f)", entry.Name()))
			}
		}
	}
}
