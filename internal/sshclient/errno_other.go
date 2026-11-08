//go:build !windows

package sshclient

import "syscall"

var (
	refusedErrnos     = []syscall.Errno{syscall.ECONNREFUSED}
	unreachableErrnos = []syscall.Errno{syscall.EHOSTUNREACH, syscall.ENETUNREACH}
	resetErrnos       = []syscall.Errno{syscall.ECONNRESET, syscall.ECONNABORTED}
)
