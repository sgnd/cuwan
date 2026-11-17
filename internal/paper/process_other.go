//go:build !unix

package paper

import (
	"os"
	"os/exec"
)

func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Windows FindProcess always succeeds; Release is best-effort.
	_ = p
	return true
}

func setDetachAttr(cmd *exec.Cmd) {
	// No setsid equivalent required for MVP detach on non-unix.
}
