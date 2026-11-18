package paper

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sgnd/cuwan/internal/store"
)

// DetachWorker starts a background `cuwan paper _worker NAME` so the grid
// keeps running after the invoking CLI exits. Survives by sealed runtime + pid.
func DetachWorker(botName string) (int, error) {
	if botName == "" {
		return 0, fmt.Errorf("paper: empty bot name")
	}
	if alive, pid := WorkerAlive(botName); alive {
		return pid, fmt.Errorf("paper: bot %q worker already running (pid %d)", botName, pid)
	}
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(exe, "paper", "_worker", botName)
	cmd.Env = childEnv()
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	setDetachAttr(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()

	// Wait briefly for pid file so callers know the worker is up.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if alive, got := WorkerAlive(botName); alive {
			return got, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return pid, nil
}

// WaitRuntime polls sealed runtime until the bot is marked running or timeout.
func WaitRuntime(botName string, d time.Duration) (*store.RuntimeState, error) {
	deadline := time.Now().Add(d)
	var last error
	for time.Now().Before(deadline) {
		rt, err := store.GetRuntime(botName)
		if err == nil && rt.Status == "running" && rt.Mode == "paper" {
			return rt, nil
		}
		last = err
		if err == nil {
			last = fmt.Errorf("paper: runtime status=%q mode=%q", rt.Status, rt.Mode)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if last == nil {
		last = fmt.Errorf("paper: runtime not ready")
	}
	return nil, last
}

func childEnv() []string {
	var out []string
	for _, e := range os.Environ() {
		switch {
		case strings.HasPrefix(e, "CUWAN_VAULT_PASS="),
			strings.HasPrefix(e, "CUWAN_KEYFILE="),
			strings.HasPrefix(e, "CUWAN_PASSPHRASE="),
			strings.HasPrefix(e, "CUWAN_UNLOCK="),
			strings.HasPrefix(e, "CUWAN_MASTER_KEY="):
			continue
		default:
			out = append(out, e)
		}
	}
	return out
}
