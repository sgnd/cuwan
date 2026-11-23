package live

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/store"
)

// longFrom95 starts a futures bot and records one filled buy at 95.
func longFrom95(t *testing.T, bot *store.Bot) (*book, *store.Runtime, Saver) {
	t.Helper()
	b := newBook(100)
	rt, save, _ := start(t, b, bot)
	b.fill(t, "buy", 95)
	b.pos = []exchangex.Position{{Symbol: "S", Side: "long", Size: 1, EntryPrice: 95}}
	if _, err := Step(context.Background(), bot, b, rt, save); err != nil {
		t.Fatal(err)
	}
	b.placed = nil
	return b, rt, save
}

func openGrid(rt *store.Runtime) int {
	n := 0
	for _, o := range rt.Orders {
		if o.Status == StatusOpen && !o.ReduceOnly {
			n++
		}
	}
	return n
}

func TestTakeProfitFlattensAndStops(t *testing.T) {
	bot := futBot()
	bot.TakeProfit = 104
	b, rt, save := longFrom95(t, bot)
	b.price = 104.5
	res, err := Step(context.Background(), bot, b, rt, save)
	if err != nil || !res.Stopped || !strings.Contains(res.Reason, "take-profit") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if NetSize(rt) != 0 || rt.Status != "stopped" || openGrid(rt) != 0 {
		t.Fatalf("net=%g status=%s open=%d", NetSize(rt), rt.Status, openGrid(rt))
	}
}

func TestStopLossFlattensAndStops(t *testing.T) {
	bot := futBot()
	bot.StopLoss = 94
	b, rt, save := longFrom95(t, bot)
	b.price = 93.5 // above the preventive close (92.815 for 20x from 95)
	res, err := Step(context.Background(), bot, b, rt, save)
	if err != nil || !res.Stopped || !strings.Contains(res.Reason, "stop-loss") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if NetSize(rt) != 0 || rt.Status != "stopped" {
		t.Fatalf("net=%g status=%s", NetSize(rt), rt.Status)
	}
}

func TestOnExitStopFlattens(t *testing.T) {
	bot := futBot()
	bot.OnExit = OnExitStop
	b, rt, save := longFrom95(t, bot)
	b.price = 112
	res, err := Step(context.Background(), bot, b, rt, save)
	if err != nil || !res.Stopped || !strings.Contains(res.Reason, "left the range") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if NetSize(rt) != 0 || openGrid(rt) != 0 {
		t.Fatalf("net=%g open=%d", NetSize(rt), openGrid(rt))
	}
}

func TestOnExitPauseCancelsThenResumes(t *testing.T) {
	for _, mode := range []string{OnExitPause, ""} { // pause is the default
		bot := *gridBot
		bot.OnExit = mode
		b := newBook(100)
		rt, save, _ := start(t, b, &bot)
		b.price = 113
		res, err := Step(context.Background(), &bot, b, rt, save)
		if err != nil || res.Stopped {
			t.Fatalf("%q: res=%+v err=%v", mode, res, err)
		}
		if openGrid(rt) != 0 || rt.Meta["paused"] == "" {
			t.Fatalf("%q: open=%d meta=%v", mode, openGrid(rt), rt.Meta)
		}
		b.placed = nil
		if _, err := Step(context.Background(), &bot, b, rt, save); err != nil || len(b.placed) != 0 {
			t.Fatalf("%q: placed while still out of range: %v %v", mode, b.placed, err)
		}
		b.price = 100
		if _, err := Step(context.Background(), &bot, b, rt, save); err != nil {
			t.Fatal(err)
		}
		if openGrid(rt) != bot.Levels || rt.Meta["paused"] != "" {
			t.Fatalf("%q: back in range: open=%d meta=%v", mode, openGrid(rt), rt.Meta)
		}
	}
}

func TestOnExitTrailMovesRange(t *testing.T) {
	bot := *gridBot // 90..110, step 5
	bot.OnExit = OnExitTrail
	b := newBook(100)
	rt, save, _ := start(t, b, &bot)
	b.price = 117
	res, err := Step(context.Background(), &bot, b, rt, save)
	if err != nil || res.Stopped {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	lo, hi := Bounds(&bot, rt)
	if lo != 100 || hi != 120 {
		t.Fatalf("range %g..%g, want 100..120", lo, hi)
	}
	if openGrid(rt) != bot.Levels {
		t.Fatalf("open %d", openGrid(rt))
	}
	for _, o := range rt.Orders {
		if o.Status == StatusOpen && (o.Price < 100 || o.Price > 120) {
			t.Fatalf("order outside new range: %+v", o)
		}
	}
	if bot.Lower != 90 {
		t.Fatal("saved bot changed; the shift lives in the runtime")
	}
}

// The preventive close fires whatever the exit settings or environment say.
func TestPreventiveCloseCannotBeSwitchedOff(t *testing.T) {
	for _, env := range []string{"CUWAN_NO_GUARD", "CUWAN_PREVENTIVE_CLOSE", "CUWAN_RISK", "CUWAN_UNSAFE"} {
		t.Setenv(env, "off")
	}
	for _, mode := range []string{"", OnExitStop, OnExitPause, OnExitTrail} {
		bot := futBot()
		bot.OnExit, bot.TakeProfit, bot.StopLoss = mode, 1e9, 91 // the stop-loss is hit too: the guard must win
		b, rt, save := longFrom95(t, bot)
		b.price = 90.4
		res, err := Step(context.Background(), bot, b, rt, save)
		if err != nil || !res.Stopped || !strings.Contains(res.Reason, "preventive") || NetSize(rt) != 0 {
			t.Fatalf("%q: res=%+v err=%v net=%g", mode, res, err, NetSize(rt))
		}
	}
}

// No package-level variable in guard.go can turn the guard off, and the bot
// record has no field that names the guard.
func TestNoGuardSwitchInCode(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "guard.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.VAR {
			t.Error("guard.go has a package-level var; the guard's numbers must be constants")
		}
	}
	for _, f := range []string{"Guard", "Preventive", "Risk", "Unsafe", "NoClose"} {
		if strings.Contains(strings.ToLower(botFields()), strings.ToLower(f)) {
			t.Errorf("store.Bot has a field like %q", f)
		}
	}
}

// Restart: a new process resumes the saved runtime by client order ID and
// does not place a second grid.
func TestResumeByClientOrderID(t *testing.T) {
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	// The last send reached the venue but the answer was lost.
	b.placeErr = func(exchangex.PlaceReq) (bool, error) { return true, context.DeadlineExceeded }
	b.fill(t, "buy", 95)
	if _, err := Step(context.Background(), gridBot, b, rt, save); err == nil {
		t.Fatal("want the lost answer as an error")
	}
	b.placeErr = nil
	if countStatus(rt, StatusPending) != 1 {
		t.Fatalf("pending %d", countStatus(rt, StatusPending))
	}
	// Process dies; the runtime is loaded again from the store.
	saved := *rt
	saved.Orders = append([]store.Order(nil), rt.Orders...)
	runID := RunID(rt)
	b.placed = nil
	if err := Resume(context.Background(), gridBot, b, &saved, save); err != nil {
		t.Fatal(err)
	}
	if countStatus(&saved, StatusPending) != 0 || RunID(&saved) != runID {
		t.Fatalf("pending=%d run=%s", countStatus(&saved, StatusPending), RunID(&saved))
	}
	if _, err := Step(context.Background(), gridBot, b, &saved, save); err != nil {
		t.Fatal(err)
	}
	if len(b.placed) != 0 {
		t.Fatalf("resume placed again: %+v", b.placed)
	}
}

func TestResumeRefusesHalted(t *testing.T) {
	b := newBook(100)
	rt, save, _ := start(t, b, gridBot)
	rt.Status = "halted"
	if err := Resume(context.Background(), gridBot, b, rt, save); err == nil {
		t.Fatal("halted runtime resumed")
	}
}

func botFields() string {
	var names []string
	ty := reflect.TypeOf(store.Bot{})
	for i := 0; i < ty.NumField(); i++ {
		names = append(names, ty.Field(i).Name)
	}
	return strings.Join(names, " ")
}
