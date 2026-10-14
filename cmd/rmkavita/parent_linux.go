//go:build linux

package main

import (
	"context"
	"os"
	"syscall"
)

// watchParent ends the app if the process that launched it goes away (AppLoad
// or xochitl restarting re-parents us to init). The qtfb socket closing is the
// normal signal and SIGTERM the usual one; this covers the case where neither
// arrives. It sleeps in the kernel on a pidfd: no timer, no wake-ups. A process
// that was started detached (parent already init) is left alone, and so is a
// kernel without pidfd_open.
func watchParent(stop context.CancelFunc) {
	p0 := os.Getppid()
	if p0 <= 1 {
		return
	}
	const sysPidfdOpen = 434 // the same number on every architecture
	fd, _, e := syscall.Syscall(sysPidfdOpen, uintptr(p0), 0, 0)
	if e != 0 {
		return
	}
	ep, err := syscall.EpollCreate1(0)
	if err != nil {
		syscall.Close(int(fd))
		return
	}
	ev := syscall.EpollEvent{Events: syscall.EPOLLIN, Fd: int32(fd)}
	if err := syscall.EpollCtl(ep, syscall.EPOLL_CTL_ADD, int(fd), &ev); err != nil {
		syscall.Close(int(fd))
		syscall.Close(ep)
		return
	}
	go func() {
		events := make([]syscall.EpollEvent, 1)
		for {
			n, err := syscall.EpollWait(ep, events, -1)
			if err == syscall.EINTR {
				continue
			}
			if err == nil && n > 0 {
				stop()
			}
			return
		}
	}()
}
