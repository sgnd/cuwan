package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/paper"
	"github.com/sgnd/cuwan/internal/store"
)

func cmdPaper(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stdout, "usage: cuwan paper start|status|stop NAME")
		return 0
	}
	switch args[0] {
	case "start":
		return paperStart(args[1:])
	case "status":
		return paperStatus(args[1:])
	case "stop":
		return paperStop(args[1:])
	case "_worker":
		// Internal: detached fill loop (invoked by paper start).
		return paperWorker(args[1:])
	default:
		return out.Fail("paper", fmt.Errorf("unknown verb %q", args[0]), 2)
	}
}

func paperStart(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("paper.start", fmt.Errorf("usage: cuwan paper start NAME [--foreground]"), 2)
	}
	name := pos[0]
	bot, err := store.GetBot(name)
	if err != nil {
		return out.Fail("paper.start", err, 1)
	}
	exRec, err := store.GetExchange(bot.Exchange)
	if err != nil {
		return out.Fail("paper.start", err, 1)
	}
	drv, err := exchangex.Open(exchangex.Config{
		Venue: exRec.Venue, Market: exRec.Market, Testnet: exRec.Testnet, Paper: true,
	})
	if err != nil {
		return out.Fail("paper.start", err, 1)
	}

	if flagBool(args, "--foreground") {
		s, err := paper.Start(context.Background(), bot, drv)
		if err != nil {
			return out.Fail("paper.start", err, 1)
		}
		out.Print(os.Stdout, "paper.start", s.Runtime, fmt.Sprintf("paper started %s (%d levels) foreground", bot.Name, len(s.Runtime.Orders)))
		waitSignals(s)
		return 0
	}

	if alive, pid := paper.WorkerAlive(name); alive {
		rt, _ := paper.Status(name)
		out.Print(os.Stdout, "paper.start", rt, fmt.Sprintf("paper already running %s pid=%d", name, pid))
		return 0
	}

	// Detached workers strip unlock env; passphrase stores cannot open in the child.
	if mode, _ := storeKeyMode(); mode == "passphrase" {
		return out.Fail("paper.start", fmt.Errorf("detached paper needs keychain/keyfile unlock (passphrase cannot be inherited); use --foreground or CUWAN_KEYCHAIN / keyfile"), 1)
	}

	pid, err := paper.DetachWorker(name)
	if err != nil {
		return out.Fail("paper.start", err, 1)
	}
	rt, err := paper.WaitRuntime(name, 8*time.Second)
	if err != nil {
		return out.Fail("paper.start", fmt.Errorf("worker pid=%d started but runtime not ready: %w", pid, err), 1)
	}
	out.Print(os.Stdout, "paper.start", rt, fmt.Sprintf("paper started %s (%d levels) detached pid=%d", bot.Name, len(rt.Orders), pid))
	return 0
}

func paperWorker(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return 2
	}
	name := pos[0]
	bot, err := store.GetBot(name)
	if err != nil {
		return 1
	}
	exRec, err := store.GetExchange(bot.Exchange)
	if err != nil {
		return 1
	}
	drv, err := exchangex.Open(exchangex.Config{
		Venue: exRec.Venue, Market: exRec.Market, Testnet: exRec.Testnet, Paper: true,
	})
	if err != nil {
		return 1
	}
	s, err := paper.Start(context.Background(), bot, drv)
	if err != nil {
		return 1
	}
	waitSignals(s)
	return 0
}

func waitSignals(s *paper.Session) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		_ = paper.Stop(s.Bot.Name)
	}()
	s.Wait()
}

func paperStatus(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("paper.status", fmt.Errorf("usage: cuwan paper status NAME"), 2)
	}
	rt, err := paper.Status(pos[0])
	if err != nil {
		return out.Fail("paper.status", err, 1)
	}
	openN, filledN := 0, 0
	for _, o := range rt.Orders {
		switch o.Status {
		case "open":
			openN++
		case "filled":
			filledN++
		}
	}
	alive, pid := paper.WorkerAlive(pos[0])
	meta := rt.Meta
	if meta == nil {
		meta = map[string]string{}
	}
	out.Print(os.Stdout, "paper.status", map[string]any{
		"runtime": rt, "worker_alive": alive, "worker_pid": pid,
	}, fmt.Sprintf("%s mode=%s status=%s open=%d filled=%d last=%s worker=%v",
		rt.BotName, rt.Mode, rt.Status, openN, filledN, meta["last_price"], alive))
	return 0
}

func paperStop(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("paper.stop", fmt.Errorf("usage: cuwan paper stop NAME [--yes]"), 2)
	}
	if !flagBool(args, "--yes") && out.Agent() {
		return out.Fail("paper.stop", fmt.Errorf("refusing without --yes"), 1)
	}
	if !flagBool(args, "--yes") && !out.Agent() {
		if !confirm("stop paper bot " + pos[0] + "?") {
			return out.Fail("paper.stop", fmt.Errorf("aborted"), 1)
		}
	}
	if err := paper.Stop(pos[0]); err != nil {
		return out.Fail("paper.stop", err, 1)
	}
	out.Print(os.Stdout, "paper.stop", map[string]string{"name": pos[0]}, "stopped "+pos[0])
	return 0
}
