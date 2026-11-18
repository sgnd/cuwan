package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sgnd/cuwan/internal/out"
	"github.com/sgnd/cuwan/internal/report"
	"github.com/sgnd/cuwan/internal/store"
)

// cmdReport prints profit per bot. The store keeps one runtime per bot name,
// so each bot shows the mode it last ran in, paper or live, not both.
func cmdReport(args []string) int {
	rate := report.DefaultFeeRate
	if v, ok := flagValue(args, "--fee"); ok {
		f, err := parseFee(v)
		if err != nil {
			return out.Fail("report", err, 2, "example: --fee 0.1%")
		}
		rate = f
	}
	var rts []store.RuntimeState
	if pos := positional(args); len(pos) > 0 {
		rt, err := store.GetRuntime(pos[0])
		if err != nil {
			return out.Fail("report", fmt.Errorf("no runtime for bot %q: %w", pos[0], err), 1,
				"start it with `cuwan paper start` or `cuwan run start`")
		}
		rts = append(rts, *rt)
	} else {
		all, err := store.ListRuntimes()
		if err != nil {
			return out.Fail("report", err, 1)
		}
		rts = all
	}
	reps := make([]report.Report, 0, len(rts))
	for i := range rts {
		reps = append(reps, report.Compute(&rts[i], rate))
	}
	out.Print(os.Stdout, "report", reps, reportText(reps, rate))
	return 0
}

// parseFee accepts "0.1%" or a fraction such as "0.001".
func parseFee(s string) (float64, error) {
	pct := strings.HasSuffix(s, "%")
	f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil || f < 0 {
		return 0, errors.New("--fee must be a rate such as 0.1% or 0.001")
	}
	if pct {
		f /= 100
	}
	return f, nil
}

func reportText(reps []report.Report, rate float64) string {
	if len(reps) == 0 {
		return "(no runtimes)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-16s %-5s %-8s %5s %5s %10s %10s %10s %10s %10s\n",
		"BOT", "MODE", "STATUS", "FILLS", "TRIPS", "NET SIZE", "REALISED", "UNREAL", "FEES", "NET")
	for _, r := range reps {
		fmt.Fprintf(&b, "%-16s %-5s %-8s %5d %5d %10.6g %10.4f %10.4f %10.4f %10.4f\n",
			r.Bot, r.Mode, r.Status, r.Fills, r.RoundTrips, r.NetSize, r.Realised, r.Unrealised, r.Fees, r.Net)
	}
	fmt.Fprintf(&b, "Fees are an estimate at %g%% of filled value; the store has no venue fees.\n", rate*100)
	b.WriteString("One runtime per bot: each row is the mode the bot last ran in.")
	return b.String()
}
