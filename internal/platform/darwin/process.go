package darwin

import "syscall"

// ProcessAlive reports whether pid names a process that exists right now.
//
// It is the liveness half of P7.2's model prune: a window whose owning process has exited has to
// leave the switcher even while a stale CoreGraphics entry still enumerates it (D46). The not-seen
// prune in internal/app catches a window that both sources have dropped; this catches one that is
// still being reported but whose process is gone.
//
// syscall.Kill(pid, 0) sends no signal — it runs only the kernel's permission-and-existence check.
// A nil result means the process is there; EPERM means it is there but owned by someone else (which a
// window's owner on this session is not, but treat it as alive rather than reap a window over a
// permissions quirk). ESRCH is the one answer that means gone. A non-positive pid is never a live
// process. Pure Go on purpose: this is POSIX, not CoreGraphics, so it needs no cgo crossing.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
