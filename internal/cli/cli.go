// Package cli wires cuwan commands.
//   - a person at a terminal may be asked for missing input
//   - an AI agent or script is never prompted; missing input is an error
package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sgnd/cuwan/internal/out"

	_ "github.com/sgnd/cuwan/internal/exchangex/binance"
	_ "github.com/sgnd/cuwan/internal/exchangex/catalog"
	_ "github.com/sgnd/cuwan/internal/exchangex/hyperliquid"
)

// Version is set at build time via -ldflags -X.
var Version = "dev"

// Execute runs cuwan with os.Args[1:] and returns the process exit code.
func Execute() int {
	return run(os.Args[1:])
}

func run(args []string) int {
	if len(args) == 0 {
		printRootHelp(os.Stdout)
		return 0
	}
	switch args[0] {
	case "version", "-v", "--version":
		return cmdVersion(args[1:])
	case "doctor":
		return cmdDoctor(args[1:])
	case "guide":
		return cmdGuide(args[1:])
	case "schema":
		return cmdSchema(args[1:])
	case "secret":
		return cmdSecret(args[1:])
	case "exchange":
		return cmdExchange(args[1:])
	case "bot":
		return cmdBot(args[1:])
	case "paper":
		return cmdPaper(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "orders":
		return cmdOrders(args[1:])
	case "positions":
		return cmdPositions(args[1:])
	case "balance":
		return cmdBalance(args[1:])
	case "backtest":
		return cmdBacktest(args[1:])
	case "audit":
		return cmdAudit(args[1:])
	case "help", "-h", "--help":
		printRootHelp(os.Stdout)
		return 0
	default:
		return out.Fail(args[0], fmt.Errorf("unknown command %q", args[0]), 2,
			"run `cuwan` for the command list")
	}
}

func printRootHelp(w io.Writer) {
	fmt.Fprint(w, strings.TrimSpace(`
cuwan: local-first CLI for CEX/DEX grid bots

State lives under ~/.cuwan (override with CUWAN_HOME). Secrets are sealed;
agents set CUWAN_AGENT=1 for JSON and no prompts.

START        version · doctor · guide · schema
VAULT        secret set|list|show|rename
VENUE        exchange add|list|venues|test
BOTS         bot add|list|show|edit|rm · draft-range (stub)
PAPER        paper start|status|stop
TEST         backtest NAME [--from] [--to] [--interval] [--fee]
LIVE         run start|status|stop
READ         orders · positions · balance · audit

Venues: live on binance; hyperliquid on testnet; other ids are catalog entries for paper.
Paper uses public tickers where wired; secrets stay secret:NAME.
Guardrails: 20x leverage cap · ~5% preventive reduce-only close (always on)
`)+"\n")
}

func confirm(prompt string) bool {
	if out.Agent() {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

func flagBool(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

func flagValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"="), true
		}
	}
	return "", false
}

func positional(args []string) []string {
	var outArgs []string
	skip := false
	for i, a := range args {
		if skip {
			skip = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				skip = true
			}
			continue
		}
		outArgs = append(outArgs, a)
	}
	return outArgs
}

func readSecretValue(args []string) (string, error) {
	if v, ok := flagValue(args, "--value"); ok {
		if out.Agent() {
			return "", fmt.Errorf("--value is refused for agents; pipe the secret on stdin")
		}
		return v, nil
	}
	if out.Agent() {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		v := strings.TrimRight(string(b), "\r\n")
		if v == "" {
			return "", fmt.Errorf("missing secret on stdin")
		}
		return v, nil
	}
	fmt.Fprint(os.Stderr, "secret value: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
