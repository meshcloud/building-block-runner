package tf

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

const (
	ERR_MSG_AUTH_PREP = "Something went wrong while initializing the git authentication method. Please try again."
)

type Git struct {
	log *logwrap
}

func (g *Git) checkoutRef(repo *git.Repository, refName string) error {
	msg := fmt.Sprintf("checking out ref %s", refName)
	g.log.PrintlnToLocalAndUpdateLogs(msg)

	currentHead, err := repo.Head()
	if err != nil {
		g.log.PrintlnToLocalAndUpdateLogs(
			err.Error(),
			fmt.Sprintf("Failed to 'git checkout' ref %s. Failed to get HEAD.", refName),
		)

		return err
	}

	g.log.PrintlnToLocalLogs(
		fmt.Sprintf("current HEAD before checkout: %s", currentHead.Hash().String()),
	)

	w, err := repo.Worktree()
	if err != nil {
		g.log.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Failed to 'git checkout' ref %s. Failed to fetch worktree.", refName))
		return err
	}

	// remote branch
	hash, err := repo.ResolveRevision(plumbing.Revision("refs/remotes/origin/" + refName))
	if err != nil {
		// tag
		hash, err = repo.ResolveRevision(plumbing.Revision("refs/tags/" + refName))
		if err != nil {
			// commit hash
			hash, err = repo.ResolveRevision(plumbing.Revision(refName))
			if err != nil {
				g.log.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Failed to 'git checkout' ref %s. Failed to resolve revision to hash.", refName))
				return err
			}
		}
	}

	if currentHead.Hash().String() == hash.String() {
		g.log.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Skipping 'git checkout' ref %s. Already on target ref (git hash: %s).", refName, hash.String()))
		return nil
	}

	err = w.Checkout(&git.CheckoutOptions{
		Hash:  *hash,
		Force: true,
	})

	if err != nil {
		g.log.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Failed to 'git checkout' ref %s. Failed to checkout ref.", refName))
		return err
	}

	return nil
}

func (g *Git) clone(auth auth, url, targetdir string) (repo *git.Repository, err error) {
	if auth.name() == AUTH_TYPE_NONE {
		repo, err = g.noAuthClone(targetdir, url)
	} else {
		repo, err = g.authClone(targetdir, url, auth)
	}

	if err != nil {
		g.log.PrintlnToLocalLogs(fmt.Sprintf("Failed to clone repository from %s to %s", url, targetdir))
	}

	return
}

func (g *Git) authClone(dir, url string, auth auth) (*git.Repository, error) {
	gitAuth, err := auth.toTransport(url, g.log)
	if err != nil {
		g.log.PrintlnToLocalAndUpdateLogs(ERR_MSG_AUTH_PREP)
		return nil, err
	}

	repo, err := git.PlainClone(dir, false, &git.CloneOptions{
		URL:  url,
		Auth: gitAuth,
	})

	if errors.Is(err, ErrResolveNoExtraKnownHost) {
		g.log.PrintlnToLocalAndUpdateLogs(ERR_MSG_KNOWN_HOST_NONE_PROVIDED)
	} else if errors.Is(err, ErrResolveGeneric) {
		g.log.PrintlnToLocalAndUpdateLogs(ERR_MSG_KNOWN_HOSTS_NO_MATCH)
	} else if err != nil {
		g.log.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Failed to 'git clone' %s with authentication.", url))
	}

	return repo, err
}

func (g *Git) noAuthClone(dir, url string) (*git.Repository, error) {
	g.log.PrintlnToLocalAndUpdateLogs("attempting to cloning remote repository without authentication.")
	repo, err := git.PlainClone(dir, false, &git.CloneOptions{
		URL: url,
	})

	if err != nil {
		g.log.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Failed to 'git clone' %s without authentication.", url))
		return nil, err
	}

	return repo, nil
}

func (g *Git) moveDirContent(src string, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		destPath := filepath.Join(dst, relativePath)

		if info.IsDir() {
			err = os.MkdirAll(destPath, info.Mode())
			if err != nil {
				return err
			}
		} else {
			err = os.Rename(path, destPath)
			if err != nil {
				return err
			}
		}

		return nil
	})
}

// Azure devops does not work with go-git:
// https://github.com/plumber-cd/terraform-backend-git/issues/15
// https://github.com/go-git/go-git/issues/328
// so to support that we invoke git via bash to checkout the repo
// You gotta love Azure.
func (g *Git) azureClone(dir string, url string, ref *string, authType AuthType) error {
	g.log.PrintlnToLocalLogs("using Azure Devops workaround")
	var err error
	var output []byte
	tmpDir := path.Join(dir, TEMP_CLONE_DIR_PATH)

	switch authType {

	case AUTH_TYPE_NONE:
		cmdcmdText := fmt.Sprintf(
			"GIT_SSH_COMMAND=\"ssh -o StrictHostKeyChecking=no\" git clone %s %s\n",
			url,
			tmpDir,
		)
		cmd := exec.Command("bash", "-c", cmdcmdText)
		output, err = cmd.CombinedOutput()
		if err != nil {
			g.log.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Git clone error: %v", err))
			g.log.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Git clone output: %s", string(output)))
		}

	case AUTH_TYPE_SSH:
		certFile := path.Join(dir, TMP_FILE_SSH_CERT)
		cmdcmdText := fmt.Sprintf(
			"GIT_SSH_COMMAND=\"ssh -i %s -o StrictHostKeyChecking=no\" git clone %s %s\n",
			certFile,
			url,
			tmpDir,
		)
		cmd := exec.Command("bash", "-c", cmdcmdText)
		output, err = cmd.CombinedOutput()
		if err != nil {
			g.log.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Git clone error: %v", err))
			g.log.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Git clone output: %s", string(output)))
		}

	default:
		return fmt.Errorf("authType '%s' not supported for Azure Devops", authType)
	}

	if err != nil {
		g.log.PrintlnToLocalAndUpdateLogs(fmt.Sprintf("Failed to 'git clone' %s with authentication.", url))
	}

	return err
}

func (g *Git) azureCheckoutRef(dir string, ref string) error {
	g.log.PrintlnToLocalLogs("checking out ref", ref)
	cmdText := fmt.Sprintf("git checkout %s\n", ref)
	cmd := exec.Command("bash", "-c", cmdText)
	cmd.Dir = dir
	_, err := cmd.Output()

	return err
}

func (g *Git) setLog(log *logwrap) {
	g.log = log
}
