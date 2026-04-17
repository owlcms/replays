//go:build windows

package jobutil

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func findUDPPortOwners(port int) ([]PortProcess, error) {
	cmd := exec.Command("netstat", "-ano", "-p", "udp")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("netstat udp owners: %w", err)
	}
	owners := parseWindowsNetstatUDPPortOwners(out, port)
	for i := range owners {
		owners[i].Command = windowsProcessName(owners[i].PID)
	}
	return owners, nil
}

func interruptProcess(pid int) error {
	return runTaskkill("/PID", strconv.Itoa(pid))
}

func terminateProcess(pid int) error {
	return runTaskkill("/T", "/PID", strconv.Itoa(pid))
}

func killProcess(pid int) error {
	return runTaskkill("/F", "/T", "/PID", strconv.Itoa(pid))
}

func runTaskkill(args ...string) error {
	cmd := exec.Command("taskkill", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func windowsProcessName(pid int) string {
	if pid <= 0 {
		return ""
	}
	cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	name := parseWindowsTasklistName(out)
	if strings.EqualFold(name, "INFO: No tasks are running which match the specified criteria.") {
		return ""
	}
	return name
}