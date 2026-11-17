//go:build !unix

package paper

import "os"

func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Windows FindProcess always succeeds; Release is best-effort.
	_ = p
	return true
}
