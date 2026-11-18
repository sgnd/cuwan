package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/sgnd/cuwan/internal/out"
)

const guideText = `cuwan agent playbook

1. Set CUWAN_AGENT=1. Never prompt; missing flags are errors.
2. Run cuwan schema for JSON shapes; cuwan doctor for home/store.
3. Secrets: echo -n VALUE | cuwan secret set NAME
   Never put secrets on argv. Output shows [hidden] / [secret:NAME].
4. Venues: cuwan exchange venues (100+ CEX/DEX ids).
   Exchange: cuwan exchange add NAME --venue ID --secret secret:NAME --market spot|usdm|perp
5. Bot: cuwan bot add NAME --exchange EX --symbol SYM --lower N --upper N --levels N [--leverage N]
   Futures leverage is capped at 20x; ~5% preventive close is always on.
6. Paper (no signed orders): cuwan paper start NAME
   Detaches a worker; status/stop talk to sealed runtime. Restart resumes open orders.
7. Live needs a human --yes confirm. Agents must not invent --yes.
8. Destructive stop/flatten/rm require --yes from a person.
9. Do not read ~/.cuwan by hand; do not change CUWAN_HOME for the user.
`

func cmdGuide(args []string) int {
	out.Print(os.Stdout, "guide", map[string]string{"text": strings.TrimSpace(guideText)}, strings.TrimSpace(guideText))
	return 0
}

func cmdSchema(args []string) int {
	schema := map[string]any{
		"envelope": map[string]any{
			"ok":      "bool",
			"command": "string",
			"data":    "object|array",
			"error":   "string",
			"hints":   "string[]",
		},
		"commands": []string{
			"version", "doctor", "guide", "schema",
			"secret.set", "secret.list", "secret.show", "secret.rename",
			"exchange.add", "exchange.list", "exchange.venues", "exchange.test",
			"bot.add", "bot.list", "bot.show", "bot.edit", "bot.rm",
			"paper.start", "paper.status", "paper.stop",
			"run.start", "run.status", "run.stop",
			"orders", "positions", "balance", "audit",
		},
		"secret_display": []string{"[hidden]", "[secret:NAME]"},
		"guardrails": map[string]any{
			"max_leverage":           20,
			"preventive_close_pct":   0.05,
			"disable_flag":           false,
		},
	}
	if out.Agent() {
		out.Print(os.Stdout, "schema", schema, "")
		return 0
	}
	fmt.Fprintln(os.Stdout, `{
  "ok": true,
  "command": "schema",
  "data": {
    "envelope": {"ok":"bool","command":"string","data":"object","error":"string","hints":"string[]"},
    "guardrails": {"max_leverage":20,"preventive_close_pct":0.05}
  }
}`)
	return 0
}
