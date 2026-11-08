//go:build windows

package sshclient

import "syscall"

// Winsock error codes; the syscall package does not export them.
const (
	wsaeconnaborted syscall.Errno = 10053
	wsaeconnreset   syscall.Errno = 10054
	wsaenetunreach  syscall.Errno = 10051
	wsaeconnrefused syscall.Errno = 10061
	wsaehostunreach syscall.Errno = 10065
)

var (
	refusedErrnos     = []syscall.Errno{syscall.ECONNREFUSED, wsaeconnrefused}
	unreachableErrnos = []syscall.Errno{syscall.EHOSTUNREACH, syscall.ENETUNREACH, wsaehostunreach, wsaenetunreach}
	resetErrnos       = []syscall.Errno{syscall.ECONNRESET, syscall.ECONNABORTED, wsaeconnreset, wsaeconnaborted}
)
