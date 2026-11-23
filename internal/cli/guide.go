package cli

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/sgnd/cuwan/internal/live"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

const guideText = `cuwan agent playbook

1. Set CUWAN_AGENT=1: JSON out, no prompts, a missing yes is an error.
2. cuwan schema lists every command; cuwan doctor checks the folder and the key store.
3. Secrets come from stdin only: printf '%s' "$KEY:$SECRET" | cuwan secret set NAME.
   Output shows [hidden] and [secret:NAME], never a value.
4. Venues: binance (spot, usdm) live; hyperliquid (perp) on --testnet only.
   cuwan exchange add NAME --venue binance --market usdm --secret NAME --testnet (or --live)
5. Bots need a size: --order-size (base asset) or --quote-amount (quote asset).
   cuwan bot add NAME --exchange EX --symbol SYM --lower N --upper N --levels N --order-size N
   Range exits: --on-exit stop|pause|trail (pause is the default), --take-profit, --stop-loss.
6. Leverage is capped at 20x. The preventive close before liquidation is always on.
7. cuwan run start NAME --dry-run is always allowed. A live start, run stop and rm need
   a person who types the yes at their terminal. An agent gets NEEDS_PERSON with the
   command to show the person; --yes does not change that.
   A futures exchange needs --leverage, a whole number from 1 to 20.
8. cuwan guide install --project (or --dry-run) adds the skill for agents; the home-wide
   install is for a person.`

func cmdGuide(args []string) int {
	if len(args) > 0 {
		return out.Fail("guide", fmt.Errorf("unknown verb %q", args[0]), 2, "cuwan guide install")
	}
	out.Print(os.Stdout, "guide", map[string]string{"text": guideText}, guideText)
	return 0
}

func cmdVersion(args []string) int {
	out.Print(os.Stdout, "version", map[string]string{"version": Version, "go": runtime.Version()}, "cuwan "+Version)
	return 0
}

func cmdSchema(args []string) int {
	s := map[string]any{
		"envelope": map[string]string{"ok": "bool", "command": "string", "data": "object|array", "error": "string", "hints": "string[]"},
		"commands": Commands(),
		"guards":   map[string]any{"max_leverage": live.MaxLeverage, "preventive_close": "always on", "switch": false},
		"secrets":  []string{"[hidden]", "[secret:NAME]"},
	}
	out.Print(os.Stdout, "schema", s, strings.Join(Commands(), "\n"))
	return 0
}

// cmdDoctor checks the folder and that the store opens with the key.
func cmdDoctor(args []string) int {
	dir, err := home()
	if err != nil {
		return out.Fail("doctor", err, 1)
	}
	d := map[string]any{"home": dir, "store": "missing (made on the first write)", "key_store": "not checked"}
	if store.Exists(dir) {
		e, err := newEnv()
		if err == nil {
			err = e.st.View(func(*store.State) error { return nil })
		}
		if err != nil {
			return out.Fail("doctor", fmt.Errorf("the store does not open: %w", err), 1)
		}
		d["store"], d["key_store"] = "opens", "ok"
	}
	out.Print(os.Stdout, "doctor", d, fmt.Sprintf("home: %s\nstore: %s\nkey store: %s", d["home"], d["store"], d["key_store"]))
	return 0
}
