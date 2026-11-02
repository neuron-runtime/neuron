//go:build windows

package command

import (
	"os/exec"
)

// runInItsOwnGroup starts the program in its own process group.
//
// Windows has no process group in the POSIX sense, so the program is started
// as it is and only the program itself can be stopped.
func runInItsOwnGroup(cmd *exec.Cmd) {}

// terminate stops the program.
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
