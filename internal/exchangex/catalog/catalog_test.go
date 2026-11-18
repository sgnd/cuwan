package catalog

import (
	"testing"

	"github.com/sgnd/cuwan/internal/exchangex"

	_ "github.com/sgnd/cuwan/internal/exchangex/binance"
	_ "github.com/sgnd/cuwan/internal/exchangex/hyperliquid"
)

func TestVenueCountAtLeast100(t *testing.T) {
	n := exchangex.VenueCount()
	if n < 100 {
		t.Fatalf("want >= 100 registered venues, got %d", n)
	}
}

func TestRequiredVenuesPresent(t *testing.T) {
	for _, id := range []string{"binance", "hyperliquid", "okx", "bybit", "uniswap", "dydx"} {
		if !exchangex.Has(id) {
			t.Fatalf("missing venue %q", id)
		}
	}
}

func TestOpenRegistryVenue(t *testing.T) {
	ex, err := exchangex.Open(exchangex.Config{Venue: "okx", Market: exchangex.MarketSpot, Paper: true})
	if err != nil {
		t.Fatal(err)
	}
	if ex.Name() != "okx" {
		t.Fatalf("name %q", ex.Name())
	}
	if _, err := ex.PlaceOrder(t.Context(), exchangex.PlaceReq{}); err == nil {
		t.Fatal("paper PlaceOrder must fail")
	}
}

func TestVenuesSortedUnique(t *testing.T) {
	list := exchangex.Venues()
	if len(list) != exchangex.VenueCount() {
		t.Fatalf("Venues len %d != count %d", len(list), exchangex.VenueCount())
	}
	seen := map[string]struct{}{}
	prev := ""
	for _, v := range list {
		if v.ID == "" {
			t.Fatal("empty venue id")
		}
		if v.ID < prev {
			t.Fatalf("unsorted: %q after %q", v.ID, prev)
		}
		if _, ok := seen[v.ID]; ok {
			t.Fatalf("duplicate %q", v.ID)
		}
		seen[v.ID] = struct{}{}
		prev = v.ID
	}
}
