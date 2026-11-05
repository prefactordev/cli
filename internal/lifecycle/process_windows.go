package lifecycle

import (
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// waitForPID waits until the process has exited.
func waitForPID(pid int) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// The process is gone.
		return nil
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	event, err := windows.WaitForSingleObject(handle, uint32(retryAttempts*retryDelay/time.Millisecond))
	if err != nil {
		return err
	}
	if event == uint32(windows.WAIT_TIMEOUT) {
		return fmt.Errorf("Timed out waiting for process %d to exit.", pid)
	}
	return nil
}

// runInstaller runs the downloaded binary with this terminal's output.
func runInstaller(path string, args []string, stdout, stderr io.Writer) error {
	cmd := exec.Command(path, args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

// startDetached starts the downloaded binary in its own process group, with
// no console, and does not wait for it.
func startDetached(path string, args []string) error {
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
