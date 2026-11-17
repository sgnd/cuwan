package paper

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/sgnd/cuwan/internal/home"
)

func TestWorkerAlivePIDFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CUWAN_HOME", filepath.Join(dir, "home"))
	if _, err := home.Ensure(); err != nil {
		t.Fatal(err)
	}
	alive, _ := WorkerAlive("nope")
	if alive {
		t.Fatal("expected dead")
	}
	p, err := pidPath("demo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	alive, pid := WorkerAlive("demo")
	if !alive || pid != os.Getpid() {
		t.Fatalf("alive=%v pid=%d want %d", alive, pid, os.Getpid())
	}
	clearPID("demo")
	alive, _ = WorkerAlive("demo")
	if alive {
		t.Fatal("expected cleared")
	}
}
