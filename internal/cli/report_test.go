package cli

import (
	"testing"

	"github.com/sgnd/cuwan/internal/store"
)

func TestReportCommand(t *testing.T) {
	setup(t, &fakeEx{})
	if code, env := call(t, "report"); code != 0 || !env.OK {
		t.Fatalf("empty report: %d %+v", code, env)
	}
	err := store.PutRuntime(&store.RuntimeState{BotName: "g1", Mode: "paper", Status: "running",
		Meta: map[string]string{"last_price": "110"},
		Orders: []store.RuntimeOrder{
			{Side: "buy", Price: 100, Qty: 1, Filled: 1, Status: "filled"},
		}})
	if err != nil {
		t.Fatal(err)
	}
	code, env := call(t, "report", "g1", "--fee", "0.1%")
	if code != 0 || !env.OK {
		t.Fatalf("report g1: %d %+v", code, env)
	}
	rows, _ := env.Data.([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["unrealised"].(float64) != 10 {
		t.Fatalf("data %+v", env.Data)
	}
	if code, _ := call(t, "report", "nope"); code != 1 {
		t.Fatalf("missing bot code %d", code)
	}
	if code, _ := call(t, "report", "--fee", "x"); code != 2 {
		t.Fatalf("bad fee code %d", code)
	}
}
