package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	alertpkg "github.com/sgnd/cuwan/internal/alert"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
	"github.com/sgnd/cuwan/internal/vault"
)

func cmdAlert(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(os.Stdout, "usage: cuwan alert set [--desktop on|off] [--webhook secret:NAME|off] [--on "+strings.Join(alertpkg.Kinds, ",")+"] | show | test")
		return 0
	}
	switch args[0] {
	case "set":
		return alertSet(args[1:])
	case "show":
		return alertShow()
	case "test":
		return alertTest()
	default:
		return out.Fail("alert", fmt.Errorf("unknown verb %q", args[0]), 2)
	}
}

func alertSet(args []string) int {
	s, err := alertpkg.Load()
	if err != nil {
		return out.Fail("alert.set", err, 1)
	}
	if v, ok := flagValue(args, "--desktop"); ok {
		switch v {
		case "on":
			s.Desktop = true
		case "off":
			s.Desktop = false
		default:
			return out.Fail("alert.set", fmt.Errorf("--desktop takes on or off"), 2)
		}
	}
	if v, ok := flagValue(args, "--webhook"); ok {
		if v == "off" {
			s.Webhook = ""
		} else if _, ok := vault.ParseRef(v); !ok {
			return out.Fail("alert.set", fmt.Errorf("--webhook takes secret:NAME, not a URL"), 2,
				"store the URL first: echo -n URL | cuwan secret set NAME")
		} else {
			s.Webhook = v
		}
	}
	if v, ok := flagValue(args, "--on"); ok {
		kinds, err := alertpkg.ParseKinds(v)
		if err != nil {
			return out.Fail("alert.set", err, 2)
		}
		s.On = kinds
	}
	if err := alertpkg.Save(s); err != nil {
		return out.Fail("alert.set", err, 1)
	}
	_ = store.AppendAudit("alert.set", map[string]any{"desktop": s.Desktop, "webhook": s.Webhook != "", "on": s.On})
	return printAlert("alert.set", s)
}

func alertShow() int {
	s, err := alertpkg.Load()
	if err != nil {
		return out.Fail("alert.show", err, 1)
	}
	return printAlert("alert.show", s)
}

// printAlert shows the webhook as [secret:NAME]; the URL itself is never read here.
func printAlert(command string, s alertpkg.Settings) int {
	hook := "off"
	if name, ok := vault.ParseRef(s.Webhook); ok {
		hook = out.SecretRef(name)
	}
	desktop := "off"
	if s.Desktop {
		desktop = "on"
	}
	out.Print(os.Stdout, command, map[string]any{
		"desktop": s.Desktop, "webhook": hook, "on": s.On,
	}, fmt.Sprintf("desktop: %s\nwebhook: %s\non: %s", desktop, hook, strings.Join(s.On, ",")))
	return 0
}

func alertTest() int {
	s, err := alertpkg.Load()
	if err != nil {
		return out.Fail("alert.test", err, 1)
	}
	ev := alertpkg.Event{Bot: "test", Kind: "test", Message: "test alert from cuwan", Time: time.Now().UTC()}
	if err := alertpkg.Send(s, ev); err != nil {
		return out.Fail("alert.test", err, 1, "see `cuwan audit` for alert.failed entries")
	}
	out.Print(os.Stdout, "alert.test", map[string]any{"sent": true}, "test alert sent")
	return 0
}
