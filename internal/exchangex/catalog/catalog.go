// Package catalog registers CEX/DEX venue ids that have no native driver yet.
//
// Backends:
//   - native: first-party drivers (binance, hyperliquid) overwrite on import
//   - public: thin public REST ticker for paper
//   - registry: id is listed and openable; public ticker / live may still be stub
//
// Coverage is a static registry plus thin adapters (not CCXT Go). Native DEX
// drivers remain first-class; the CEX ids follow gocryptotrader's list.
package catalog

import (
	"github.com/sgnd/cuwan/internal/exchangex"
	"github.com/sgnd/cuwan/internal/exchangex/publictick"
)

type entry struct {
	id      string
	kind    string
	markets []string
	live    string // paper-only | stub | full (native fills later)
	ticker  *publictick.Spec
}

func init() {
	for _, e := range allEntries() {
		info := exchangex.VenueInfo{
			ID:           e.id,
			Kind:         e.kind,
			Markets:      append([]string(nil), e.markets...),
			Backend:      exchangex.BackendRegistry,
			PublicTicker: e.ticker != nil && e.ticker.URLTemplate != "",
			Live:         e.live,
		}
		if info.PublicTicker {
			info.Backend = exchangex.BackendPublic
		}
		spec := e.ticker
		id := e.id
		exchangex.RegisterMeta(info, func(cfg exchangex.Config) (exchangex.Exchange, error) {
			meta, ok := exchangex.Lookup(id)
			if !ok {
				meta = info
			}
			return publictick.Open(cfg, meta, spec)
		})
	}
}

func spot(id string, tick *publictick.Spec) entry {
	live := "stub"
	if tick != nil && tick.URLTemplate != "" {
		live = "paper-only"
	}
	return entry{id: id, kind: exchangex.KindCEX, markets: []string{exchangex.MarketSpot}, live: live, ticker: tick}
}

func spotFut(id string, tick *publictick.Spec) entry {
	live := "stub"
	if tick != nil && tick.URLTemplate != "" {
		live = "paper-only"
	}
	return entry{
		id: id, kind: exchangex.KindCEX,
		markets: []string{exchangex.MarketSpot, exchangex.MarketUSDM},
		live:    live, ticker: tick,
	}
}

func perpDEX(id string, tick *publictick.Spec) entry {
	live := "stub"
	if tick != nil && tick.URLTemplate != "" {
		live = "paper-only"
	}
	return entry{id: id, kind: exchangex.KindDEX, markets: []string{exchangex.MarketPerp}, live: live, ticker: tick}
}

func spotDEX(id string) entry {
	return entry{id: id, kind: exchangex.KindDEX, markets: []string{exchangex.MarketSpot}, live: "stub"}
}

func allEntries() []entry {
	okxDash := &publictick.Spec{
		URLTemplate: "https://www.okx.com/api/v5/market/ticker?instId={symbol}",
		SymbolFmt:   "dash", PricePath: "data.0.last",
	}
	bybit := &publictick.Spec{
		URLTemplate: "https://api.bybit.com/v5/market/tickers?category=spot&symbol={symbol}",
		SymbolFmt:   "upper_concat", PricePath: "result.list.0.lastPrice",
	}
	kucoin := &publictick.Spec{
		URLTemplate: "https://api.kucoin.com/api/v1/market/orderbook/level1?symbol={symbol}",
		SymbolFmt:   "dash", PricePath: "data.price",
	}
	gate := &publictick.Spec{
		URLTemplate: "https://api.gateio.ws/api/v4/spot/tickers?currency_pair={symbol}",
		SymbolFmt:   "underscore", PricePath: "0.last", ArrayPrice: true,
	}
	mexc := &publictick.Spec{
		URLTemplate: "https://api.mexc.com/api/v3/ticker/price?symbol={symbol}",
		SymbolFmt:   "upper_concat", PricePath: "price",
	}
	bitget := &publictick.Spec{
		URLTemplate: "https://api.bitget.com/api/v2/spot/market/tickers?symbol={symbol}",
		SymbolFmt:   "upper_concat", PricePath: "data.0.lastPr",
	}
	htx := &publictick.Spec{
		URLTemplate: "https://api.huobi.pro/market/detail/merged?symbol={symbol}",
		SymbolFmt:   "lower_concat", PricePath: "tick.close",
	}
	coinbase := &publictick.Spec{
		URLTemplate: "https://api.exchange.coinbase.com/products/{symbol}/ticker",
		SymbolFmt:   "dash", PricePath: "price",
	}
	gemini := &publictick.Spec{
		URLTemplate: "https://api.gemini.com/v1/pubticker/{symbol}",
		SymbolFmt:   "lower_concat", PricePath: "last",
	}
	bitstamp := &publictick.Spec{
		URLTemplate: "https://www.bitstamp.net/api/v2/ticker/{symbol}/",
		SymbolFmt:   "lower_concat", PricePath: "last",
	}

	entries := []entry{
		// binance + hyperliquid are registered by native packages (not here).
		spot("binanceus", &publictick.Spec{
			URLTemplate: "https://api.binance.us/api/v3/ticker/price?symbol={symbol}",
			SymbolFmt:   "upper_concat", PricePath: "price",
		}),
		spotFut("okx", okxDash),
		spotFut("bybit", bybit),
		spot("kucoin", kucoin),
		spot("gate", gate),
		spot("gateio", gate),
		spot("mexc", mexc),
		spot("bitget", bitget),
		spot("htx", htx),
		spot("huobi", htx),
		spot("coinbase", coinbase),
		spot("coinbaseexchange", coinbase),
		spot("gemini", gemini),
		spot("bitstamp", bitstamp),
		spot("kraken", &publictick.Spec{
			URLTemplate: "https://api.kraken.com/0/public/Ticker?pair={symbol}",
			SymbolFmt:   "raw", PricePath: "result.XBTUSDT.c.0",
		}),
		spot("poloniex", &publictick.Spec{
			URLTemplate: "https://api.poloniex.com/markets/{symbol}/price",
			SymbolFmt:   "underscore", PricePath: "price",
		}),
		spot("coinex", &publictick.Spec{
			URLTemplate: "https://api.coinex.com/v1/market/ticker?market={symbol}",
			SymbolFmt:   "upper_concat", PricePath: "data.ticker.last",
		}),
		spot("ascendex", &publictick.Spec{
			URLTemplate: "https://ascendex.com/api/pro/v1/ticker?symbol={symbol}",
			SymbolFmt:   "slash", PricePath: "data.close",
		}),
		spot("bitmart", &publictick.Spec{
			URLTemplate: "https://api-cloud.bitmart.com/spot/quotation/v3/ticker?symbol={symbol}",
			SymbolFmt:   "underscore", PricePath: "data.last",
		}),

		// Additional CEX (registry; paper ticker adapters welcome)
		spot("btse", nil),
		spot("bitmex", nil),
		spot("deribit", nil),
		spot("phemex", nil),
		spot("cryptocom", nil),
		spot("hitbtc", nil),
		spot("bitflyer", nil),
		spot("bithumb", nil),
		spot("btcmarkets", nil),
		spot("yobit", nil),
		spot("luno", nil),
		spot("independentreserve", nil),
		spot("btcbox", nil),
		spot("coincheck", nil),
		spot("zaif", nil),
		spot("bitso", nil),
		spot("mercado", nil),
		spot("indodax", nil),
		spot("upbit", nil),
		spot("coinone", nil),
		spot("probit", nil),
		spot("bitvavo", nil),
		spot("exmo", nil),
		spot("zonda", nil),
		spot("paymium", nil),
		spot("ndax", nil),
		spot("oceanex", nil),
		spot("latoken", nil),
		spot("bigone", nil),
		spot("bitrue", nil),
		spot("tokocrypto", nil),
		spot("coinsph", nil),
		spot("bitopro", nil),
		spot("delta", nil),
		spot("woo", nil),
		spot("xt", nil),
		spot("bingx", nil),
		spot("blofin", nil),
		spot("hashkey", nil),
		spot("hollaex", nil),
		spot("digifinex", nil),
		spot("fmfwio", nil),
		spot("bequant", nil),
		spot("cex", nil),
		spot("coinmate", nil),
		spot("coinmetro", nil),
		spot("coinspot", nil),
		spot("btcturk", nil),
		spot("btcalpha", nil),
		spot("bitbank", nil),
		spot("bitbns", nil),
		spot("bitteam", nil),
		spot("bittrade", nil),
		spot("blockchaincom", nil),
		spot("coincatch", nil),
		spot("cryptomus", nil),
		spot("defx", nil),
		spot("myokx", nil),
		spot("novadax", nil),
		spot("okcoin", nil),
		spot("okxus", nil),
		spot("onetrading", nil),
		spot("oxfun", nil),
		spot("p2b", nil),
		spot("timex", nil),
		spot("tradeogre", nil),
		spot("wavesexchange", nil),
		spot("alpaca", nil),
		spot("apex", nil),
		spot("ace", nil),
		spot("kuna", nil),
		spot("kucoinfutures", nil),
		spot("krakenfutures", nil),
		spot("binanceusdm", nil),
		spot("binancecoinm", nil),
		spot("coinbaseinternational", nil),
		spot("coinbaseadvanced", nil),
		spot("derive", nil),
		spot("paradex", nil),
		spot("vertex", nil),
		spot("woofipro", nil),
		spot("lbank", nil),
		spot("whitebit", nil),
		spot("bitfinex", nil),
		spot("zb", nil),
		spot("coinut", nil),
		spot("itbit", nil),
		spot("lakebtc", nil),
		spot("okex", nil),
		spot("okcoinintl", nil),
		spot("huobipro", nil),

		// DEX / perps (hyperliquid has a native driver, so it is not listed here)
		perpDEX("dydx", nil),
		perpDEX("dydxv4", nil),
		perpDEX("gmx", nil),
		perpDEX("aevo", nil),
		perpDEX("drift", nil),
		perpDEX("mango", nil),
		perpDEX("jupiterperp", nil),
		perpDEX("gmxv2", nil),
		perpDEX("vertexdex", nil),
		perpDEX("rabbitx", nil),
		perpDEX("apexomni", nil),
		perpDEX("bluefin", nil),
		perpDEX("basedapp", nil),
		perpDEX("lighter", nil),
		perpDEX("ethereal", nil),
		spotDEX("uniswap"),
		spotDEX("sushiswap"),
		spotDEX("pancakeswap"),
		spotDEX("raydium"),
		spotDEX("orca"),
		spotDEX("jupiter"),
		spotDEX("curve"),
		spotDEX("balancer"),
		spotDEX("1inch"),
		spotDEX("0x"),
		spotDEX("kyberswap"),
		spotDEX("traderjoe"),
		spotDEX("quickswap"),
		spotDEX("spookyswap"),
		spotDEX("velodrome"),
		spotDEX("aerodrome"),
		spotDEX("camelot"),
		spotDEX("thruster"),
		spotDEX("lfj"),
		spotDEX("phaserswap"),
		spotDEX("sunswap"),
		spotDEX("biswap"),
		spotDEX("traderjoev2"),
		spotDEX("ramses"),
		spotDEX("solidly"),
	}
	return dedupe(entries)
}

func dedupe(in []entry) []entry {
	seen := map[string]struct{}{}
	out := make([]entry, 0, len(in))
	for _, e := range in {
		if _, ok := seen[e.id]; ok {
			continue
		}
		seen[e.id] = struct{}{}
		out = append(out, e)
	}
	return out
}
