// Package cli wires cuwan's commands.
//   - a person at a terminal may be asked to confirm
//   - an AI agent or a script is never prompted; a missing yes is an error
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/sgnd/cuwan/internal/exchangex"
	_ "github.com/sgnd/cuwan/internal/exchangex/binance"     // registers the Binance driver
	_ "github.com/sgnd/cuwan/internal/exchangex/hyperliquid" // registers the Hyperliquid driver
	"github.com/sgnd/cuwan/internal/keychain"
	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/store"
)

// Version is set at build time with -ldflags -X.
var Version = "dev"

// Seams that tests replace.
var (
	masterKey  = keychain.Key
	openDriver = exchangex.Open
	isAgent    = out.Agent
	openTTY    = func() (io.ReadWriteCloser, error) {
		return os.OpenFile("/dev/tty", os.O_RDWR, 0)
	}
)

// handler runs one command with the arguments after its name.
type handler func(args []string) int

// table maps "noun" or "noun verb" to its handler. Its keys are the
// command list: schema prints it, and tests compare it with SKILL.md and
// AGENTS.md.
var table map[string]handler

func init() {
	table = map[string]handler{
		"version": cmdVersion, "doctor": cmdDoctor, "guide": cmdGuide, "guide install": cmdGuideInstall, "schema": cmdSchema,
		"secret set": secretSet, "secret list": secretList, "secret show": secretShow, "secret rm": secretRm,
		"exchange add": exchangeAdd, "exchange list": exchangeList, "exchange test": exchangeTest,
		"bot add": botAdd, "bot list": botList, "bot show": botShow, "bot edit": botEdit, "bot rm": botRm,
		"run start": runStart, "run status": runStatus, "run stop": runStop,
		"orders": cmdOrders, "positions": cmdPositions, "balance": cmdBalance, "audit": cmdAudit,
	}
}

// Commands lists every command, sorted, as "noun verb".
func Commands() []string {
	out := make([]string, 0, len(table))
	for k := range table {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Execute runs cuwan with os.Args[1:] and returns the exit code.
func Execute() int { return run(os.Args[1:]) }

func run(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printHelp()
		return 0
	}
	if args[0] == "-v" || args[0] == "--version" {
		args[0] = "version"
	}
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		if h, ok := table[args[0]+" "+args[1]]; ok {
			return h(args[2:])
		}
	}
	if h, ok := table[args[0]]; ok {
		return h(args[1:])
	}
	for k := range table {
		if strings.HasPrefix(k, args[0]+" ") {
			return out.Fail(args[0], fmt.Errorf("unknown verb %q", strings.Join(args[1:2], "")), 2, "run `cuwan` for the command list")
		}
	}
	return out.Fail(args[0], fmt.Errorf("unknown command %q", args[0]), 2, "run `cuwan` for the command list")
}

func printHelp() {
	fmt.Println(`cuwan: grid bots on your own machine, for people and AI agents

State lives in ~/.cuwan (CUWAN_HOME), sealed with a key in the system key
store. AI agents set CUWAN_AGENT=1 for JSON and no prompts.

Commands:`)
	for _, c := range Commands() {
		fmt.Println("  cuwan " + c)
	}
	fmt.Println(`
Venues: Binance (spot, USD-M) live; Hyperliquid perps on testnet only.
Guards: 20x leverage cap and a preventive close before liquidation, always on.`)
}

// env is an opened store in the cuwan folder.
type env struct {
	dir string
	st  *store.Store
}

func home() (string, error) {
	if h := os.Getenv("CUWAN_HOME"); h != "" {
		return h, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".cuwan"), nil
}

// newEnv opens the store, making its key on first use.
func newEnv() (*env, error) {
	dir, err := home()
	if err != nil {
		return nil, err
	}
	key, err := masterKey(!store.Exists(dir))
	if err != nil {
		return nil, err
	}
	st, err := store.Open(dir, key)
	if err != nil {
		return nil, err
	}
	return &env{dir: dir, st: st}, nil
}

// audit appends one entry; a failed write fails the command.
func (e *env) audit(command string, fields map[string]any) error {
	rec := map[string]any{"command": command, "agent": out.Agent()}
	for k, v := range fields {
		rec[k] = v
	}
	return e.st.Update(func(s *store.State) error { return s.Append(rec) })
}

// withEnv opens the store or fails the command.
func withEnv(cmd string, fn func(e *env) int) int {
	e, err := newEnv()
	if err != nil {
		return out.Fail(cmd, err, 1)
	}
	return fn(e)
}

// flags parses --name value and --name pairs. Every flag must be known.
type flags struct {
	pos  []string
	vals map[string]string
	set  map[string]bool
}

func parse(args []string, valued, bools []string) (*flags, error) {
	f := &flags{vals: map[string]string{}, set: map[string]bool{}}
	isVal := map[string]bool{}
	for _, v := range valued {
		isVal[v] = true
	}
	isBool := map[string]bool{}
	for _, b := range bools {
		isBool[b] = true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			f.pos = append(f.pos, a)
			continue
		}
		name, val, hasEq := strings.Cut(a[2:], "=")
		switch {
		case isBool[name] && !hasEq:
			f.set[name] = true
		case isVal[name] && hasEq:
			f.vals[name], f.set[name] = val, true
		case isVal[name] && i+1 < len(args):
			i++
			f.vals[name], f.set[name] = args[i], true
		case isVal[name]:
			return nil, fmt.Errorf("--%s needs a value", name)
		default:
			return nil, fmt.Errorf("unknown flag --%s", name)
		}
	}
	return f, nil
}

func (f *flags) num(name string) (float64, error) {
	v, err := strconv.ParseFloat(f.vals[name], 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("--%s must be a finite number", name)
	}
	return v, nil
}

// needOne returns the single positional argument or a usage error.
func needOne(f *flags, usage string) (string, error) {
	if len(f.pos) != 1 {
		return "", errors.New("usage: " + usage)
	}
	return f.pos[0], nil
}

// confirm asks the person at the terminal. An AI agent never gives the
// yes, with or without --yes: it gets NEEDS_PERSON and the command for the
// person to type. The answer is read from the terminal device, never from
// stdin, and without a terminal there is no yes. --yes skips the question
// only for a person at a terminal.
func confirm(f *flags, command, question string) error {
	if isAgent() {
		return fmt.Errorf("NEEDS_PERSON: a person runs this at their terminal: %s", command)
	}
	tty, err := openTTY()
	if err != nil {
		return fmt.Errorf("this needs a person at a terminal (%v); nothing was done", err)
	}
	defer func() { _ = tty.Close() }()
	if f.set["yes"] {
		return nil
	}
	_, _ = fmt.Fprintf(tty, "%s [y/N] ", question)
	line, _ := bufio.NewReader(tty).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "y" || line == "yes" {
		return nil
	}
	return errors.New("not confirmed; nothing was done")
}
