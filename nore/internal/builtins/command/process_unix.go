//go:build !windows

package command

import (
	"errors"
	"os/exec"
	"syscall"
)

// runInItsOwnGroup starts the program in its own process group.
//
// The group is what makes a deadline or a cancellation reach everything the
// program started: a program that leaves a child behind does not get to keep
// it once the run it belongs to is over.
func runInItsOwnGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate stops the program and everything it started.
//
// The group is signalled rather than the program alone, so a child that
// outlived its parent is stopped as well.
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}

	// A negative pid addresses the process group the program leads.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return cmd.Process.Kill()
	}
	return nil
}
