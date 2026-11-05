//go:build !windows

package lifecycle

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// waitForPID waits until the process has exited.
func waitForPID(pid int) error {
	for range retryAttempts {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.Sleep(retryDelay)
	}
	return fmt.Errorf("Timed out waiting for process %d to exit.", pid)
}

// runInstaller runs the downloaded binary with this terminal's output.
func runInstaller(path string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.Command(path, args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

// startDetached is only used on Windows.
func startDetached(path string, args []string) error {
	return errors.New("detached installs are only used on Windows")
}
