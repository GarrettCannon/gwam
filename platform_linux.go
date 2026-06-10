//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// setOPOST re-enables OPOST/ONLCR on the given tty fd so the kernel translates
// outgoing "\n" to "\r\n". See platform_darwin.go for the full rationale.
func setOPOST(fd int) {
	if t, e := unix.IoctlGetTermios(fd, unix.TCGETS); e == nil {
		t.Oflag |= unix.OPOST | unix.ONLCR
		_ = unix.IoctlSetTermios(fd, unix.TCSETS, t)
	}
}

// fgCmdOfPgid reads /proc/<pid>/comm — the kernel-maintained command name
// (truncated to 15 chars by the kernel).
func fgCmdOfPgid(pgid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pgid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(data), "\n")
}

// cwdOfPID reads /proc/<pid>/cwd — a symlink to the process's working dir.
func cwdOfPID(pid int) string {
	if pid <= 0 {
		return ""
	}
	p, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
	if err != nil {
		return ""
	}
	return p
}
