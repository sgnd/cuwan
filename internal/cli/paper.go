package cli

import (
	"context"
	"fmt"
	"os"

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
	default:
		return out.Fail("paper", fmt.Errorf("unknown verb %q", args[0]), 2)
	}
}

func paperStart(args []string) int {
	pos := positional(args)
	if len(pos) < 1 {
		return out.Fail("paper.start", fmt.Errorf("usage: cuwan paper start NAME"), 2)
	}
	bot, err := store.GetBot(pos[0])
	if err != nil {
		return out.Fail("paper.start", err, 1)
	}
	_, drv, err := openNamedExchange(bot.Exchange, true)
	if err != nil {
		return out.Fail("paper.start", err, 1)
	}
	// Force paper flag on driver config by reopening
	exRec, _ := store.GetExchange(bot.Exchange)
	drv, err = exchangex.Open(exchangex.Config{
		Venue: exRec.Venue, Market: exRec.Market, Testnet: exRec.Testnet, Paper: true,
	})
	if err != nil {
		return out.Fail("paper.start", err, 1)
	}
	s, err := paper.Start(context.Background(), bot, drv)
	if err != nil {
		return out.Fail("paper.start", err, 1)
	}
	out.Print(os.Stdout, "paper.start", s.Runtime, fmt.Sprintf("paper started %s (%d levels)", bot.Name, len(s.Runtime.Orders)))
	return 0
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
	out.Print(os.Stdout, "paper.status", rt, fmt.Sprintf("%s mode=%s status=%s open=%d filled=%d last=%s",
		rt.BotName, rt.Mode, rt.Status, openN, filledN, rt.Meta["last_price"]))
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
		if !confirm("stop paper bot "+pos[0]+"?") {
			return out.Fail("paper.stop", fmt.Errorf("aborted"), 1)
		}
	}
	if err := paper.Stop(pos[0]); err != nil {
		return out.Fail("paper.stop", err, 1)
	}
	out.Print(os.Stdout, "paper.stop", map[string]string{"name": pos[0]}, "stopped "+pos[0])
	return 0
}
