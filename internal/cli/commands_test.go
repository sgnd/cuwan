package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSecretAgentFlow(t *testing.T) {
	setup(t, &fakeEx{})
	if code, env := call(t, "secret", "set", "k", "--value", "plain"); code == 0 || !strings.Contains(env.Error, "stdin") {
		t.Fatalf("argv secret accepted: %d %+v", code, env)
	}
	if code, _ := callIn(t, "", "secret", "set", "k"); code == 0 {
		t.Fatal("empty stdin accepted")
	}
	code, env := callIn(t, "s3cr3t-value\n", "secret", "set", "k")
	if code != 0 {
		t.Fatalf("set: %+v", env)
	}
	for _, args := range [][]string{{"secret", "list"}, {"secret", "show", "k"}, {"secret", "show", "k", "--reveal"}} {
		code, env := call(t, args...)
		b, _ := json.Marshal(env)
		if code != 0 || strings.Contains(string(b), "s3cr3t") {
			t.Fatalf("%v leaked or failed: %s", args, b)
		}
	}
	if code, _ := call(t, "secret", "rename", "k", "k2"); code != 0 {
		t.Fatal("rename")
	}
	if code, _ := call(t, "secret", "show", "k"); code == 0 {
		t.Fatal("old name still resolves")
	}
}

func TestExchangeAddValidation(t *testing.T) {
	setup(t, &fakeEx{})
	cases := map[string][]string{
		"missing --venue":  {"exchange", "add", "zz"},
		"unknown venue":    {"exchange", "add", "x", "--venue", "nope-venue"},
		"missing --secret": {"exchange", "add", "x", "--venue", "binance"},
		"secret:NAME":      {"exchange", "add", "x", "--venue", "binance", "--secret", "rawkey"},
	}
	for want, args := range cases {
		code, env := call(t, args...)
		if code != 2 || !strings.Contains(env.Error, want) {
			t.Errorf("%v: code=%d err=%q want %q", args, code, env.Error, want)
		}
	}
	code, env := call(t, "exchange", "add", "bn", "--venue", "binance", "--secret", "secret:k", "--testnet")
	if code != 0 {
		t.Fatalf("add: %+v", env)
	}
	_, env = call(t, "exchange", "list")
	b, _ := json.Marshal(env.Data)
	if !strings.Contains(string(b), `"bn"`) || !strings.Contains(string(b), "[secret:k]") {
		t.Fatalf("list: %s", b)
	}
	_, env = call(t, "exchange", "venues")
	if m, _ := env.Data.(map[string]any); m["count"].(float64) < 100 {
		t.Fatalf("venues: %v", m["count"])
	}
}

func TestBotLifecycle(t *testing.T) {
	setup(t, &fakeEx{})
	bad := [][]string{
		{"bot", "add", "b"},
		{"bot", "add", "b", "--exchange", "fx", "--symbol", "S", "--lower", "10", "--upper", "5", "--levels", "4"},
		{"bot", "add", "b", "--exchange", "fx", "--symbol", "S", "--lower", "1", "--upper", "5", "--levels", "1"},
		{"bot", "add", "b", "--exchange", "fx", "--symbol", "S", "--lower", "x", "--upper", "5", "--levels", "4"},
		{"bot", "add", "b", "--exchange", "fx", "--symbol", "S", "--lower", "1", "--upper", "5", "--levels", "4", "--size", "-1"},
	}
	for _, args := range bad {
		if code, env := call(t, args...); code != 2 {
			t.Errorf("%v accepted: %+v", args, env)
		}
	}
	code, env := call(t, "bot", "add", "b", "--exchange", "fx", "--symbol", "S", "--lower", "1", "--upper", "5", "--levels", "4", "--leverage", "99")
	if code != 0 || env.Data.(map[string]any)["leverage"].(float64) != 20 {
		t.Fatalf("add: %d %+v", code, env)
	}
	if code, _ := call(t, "bot", "edit", "b", "--upper", "0.5"); code != 2 {
		t.Fatal("edit to bad range accepted")
	}
	if code, env := call(t, "bot", "edit", "b", "--levels", "6"); code != 0 || env.Data.(map[string]any)["levels"].(float64) != 6 {
		t.Fatalf("edit: %+v", env)
	}
	if _, env := call(t, "bot", "list"); len(env.Data.([]any)) != 2 {
		t.Fatalf("list: %+v", env.Data)
	}
	if code, _ := call(t, "bot", "rm", "b"); code == 0 {
		t.Fatal("rm without --yes")
	}
	if code, _ := call(t, "bot", "rm", "b", "--yes"); code != 0 {
		t.Fatal("rm")
	}
	if code, _ := call(t, "bot", "show", "b"); code == 0 {
		t.Fatal("removed bot still shown")
	}
}

func TestReadOnlyCommands(t *testing.T) {
	setup(t, &fakeEx{})
	for _, args := range [][]string{{"version"}, {"doctor"}, {"guide"}, {"schema"}, {"audit"}, {"orders", "fx"}, {"balance", "fx"}, {"positions", "fx"}} {
		if code, env := call(t, args...); code != 0 || !env.OK {
			t.Errorf("%v: %d %+v", args, code, env)
		}
	}
}

// Every command in the schema must dispatch (no "unknown command").
func TestSchemaMatchesDispatch(t *testing.T) {
	t.Setenv("CUWAN_AGENT", "1")
	_, env := call(t, "schema")
	cmds := env.Data.(map[string]any)["commands"].([]any)
	for _, c := range cmds {
		parts := strings.SplitN(c.(string), ".", 2)
		args := append(parts, "--bogus-unknown-verb-check")
		if len(parts) == 2 {
			args = []string{parts[0], "zz-unknown-verb"}
			_, env := call(t, args...)
			if !strings.Contains(env.Error, "unknown verb") {
				t.Errorf("%s: group missing: %+v", c, env)
			}
			continue
		}
		_, env := call(t, args[0])
		if strings.Contains(env.Error, "unknown command") {
			t.Errorf("%s not dispatched", c)
		}
	}
}

func TestPaperCommandGuards(t *testing.T) {
	setup(t, &fakeEx{})
	if code, _ := call(t, "paper", "stop", "g"); code == 0 {
		t.Fatal("agent paper stop without --yes")
	}
	if code, _ := call(t, "paper", "status", "nobody"); code == 0 {
		t.Fatal("status of unknown bot")
	}
	if code, _ := call(t, "paper", "start", "nobody"); code == 0 {
		t.Fatal("start of unknown bot")
	}
}

func TestExchangeTest(t *testing.T) {
	fx := &fakeEx{}
	setup(t, fx)
	code, env := call(t, "exchange", "test", "fx")
	if code != 0 || env.Data.(map[string]any)["price"].(float64) != 100 {
		t.Fatalf("%d %+v", code, env)
	}
	fx.perms.CanWithdraw = true
	if code, env := call(t, "exchange", "test", "fx"); code == 0 || !strings.Contains(env.Error, "withdrawal") {
		t.Fatalf("withdraw key passed: %+v", env)
	}
}
