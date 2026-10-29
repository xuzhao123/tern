//go:build linux

package appserver

import "syscall"

func serviceProcessAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGINT}
}
