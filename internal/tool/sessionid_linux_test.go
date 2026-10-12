//go:build linux

package tool

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// sessionOf returns the session a process belongs to, read from /proc: the
// standard library's syscall package has no Getsid on Linux. Fields after the
// parenthesised command name are: state, ppid, pgrp, session.
func sessionOf(pid int) (int, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	s := string(raw)
	end := strings.LastIndexByte(s, ')')
	if end < 0 {
		return 0, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(s[end+1:])
	if len(fields) < 4 {
		return 0, fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	return strconv.Atoi(fields[3])
}
