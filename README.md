# cuwan

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![CI](https://github.com/sgnd/cuwan/actions/workflows/ci.yml/badge.svg)](https://github.com/sgnd/cuwan/actions/workflows/ci.yml)

**One local command line for your grid trading bots on crypto exchanges. For you and your AI agent.**

A grid bot places buy and sell orders at fixed price levels and refills
each side as the price moves. Hosted services run these bots for you and
hold your exchange keys. cuwan runs them on your own machine instead, and
you and an AI agent (Claude Code, Codex, Cursor, your own bot) can both
drive it:

- **Your keys stay in an encrypted vault** under `~/.cuwan`. An AI agent
  sees `[secret:NAME]`, never the value.
- **Try a grid on paper first.** Paper mode uses public prices and never
  sends a signed order.
- **Live orders need a person's yes.** cuwan refuses a key that can
  withdraw, caps leverage at 20x, and closes a position before it nears
  liquidation. The close cannot be turned off.
- **Readable text for you, JSON for an agent** (`CUWAN_AGENT=1`).
- **One file to install**, no cloud runtime. cuwan is not a thin client
  to [efolusi.com](https://efolusi.com).

It is built like [komando](https://github.com/sgnd/komando): one static
binary, a sealed local store, and secrets referred to as `secret:NAME`.

## Install

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/sgnd/cuwan/main/install.sh | sh
```

Or from source:

```bash
git clone https://github.com/sgnd/cuwan.git
cd cuwan && make build
./bin/cuwan version
```

## Venues

```bash
cuwan exchange venues                 # list every registered venue id
cuwan exchange add my-okx --venue okx --secret secret:okx-main --market spot
```

Which venues work today:

| Venue | Paper | Live |
|---|---|---|
| Binance spot and USD-M | yes | yes; the first run on the Binance testnet is still to come |
| Hyperliquid perps | yes | testnet only, until its testnet run is done |
| Every other id in `cuwan exchange venues` | public prices where the venue has them | no: catalog entries only |

`cuwan run start` refuses a live run on a venue that is not marked `full`,
unless the exchange is set to its testnet.
Secrets stay sealed refs (`secret:NAME`) and are never shown raw to agents.

## First commands

```bash
cuwan doctor                              # create ~/.cuwan, check sealed store
cuwan secret set binance-main             # keys never on argv in agent mode
cuwan exchange add binance --venue binance --secret secret:binance-main --market spot

cuwan bot add btc-grid --exchange binance --symbol BTCUSDT \
  --lower 90000 --upper 110000 --levels 10
cuwan paper start btc-grid                # public market data; no signed orders
cuwan paper status btc-grid
```

Live trading needs an explicit person confirm. Futures leverage is capped at
**20x**; a **~5%** preventive reduce-only close vs estimated liquidation is
always on.

## Agents

Set `CUWAN_AGENT=1`. Start with `cuwan guide` and `cuwan schema`. Secrets print
as `[hidden]` / `[secret:NAME]`. See [AGENTS.md](AGENTS.md) and
[skills/cuwan/SKILL.md](skills/cuwan/SKILL.md).

## License

[AGPL-3.0](LICENSE). Companies that need use without copyleft: see
[COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md). Contributors sign the
[CLA](CLA.md).

© Sugeng Agung Suganda
