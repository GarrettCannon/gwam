//go:build darwin

package main

import (
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// setOPOST re-enables OPOST/ONLCR on the given tty fd so the kernel translates
// outgoing "\n" to "\r\n". term.MakeRaw clears these; bubbletea writes plain
// LFs between rendered rows assuming the kernel will add CRs, so without this
// every row after the first starts at the previous row's trailing column.
func setOPOST(fd int) {
	if t, e := unix.IoctlGetTermios(fd, unix.TIOCGETA); e == nil {
		t.Oflag |= unix.OPOST | unix.ONLCR
		_ = unix.IoctlSetTermios(fd, unix.TIOCSETA, t)
	}
}

// fgCmdOfPgid returns the process name for the given pgid via sysctl.
func fgCmdOfPgid(pgid int) string {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pgid)
	if err != nil {
		return ""
	}
	b := kp.Proc.P_comm[:]
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

// cwdOfPID resolves a process's working directory by shelling out to lsof —
// portable across macOS without cgo and fast enough to run per-poll.
func cwdOfPID(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return line[1:]
		}
	}
	return ""
}
