//go:build !unix

package process

import "os/exec"

func startInOwnGroup(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}
