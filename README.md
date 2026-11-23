# cuwan

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![CI](https://github.com/sgnd/cuwan/actions/workflows/ci.yml/badge.svg)](https://github.com/sgnd/cuwan/actions/workflows/ci.yml)

**One local command line for your grid trading bots on crypto exchanges. For you and your AI agent.**

A grid bot places buy and sell orders at fixed price levels and refills
each side as the price moves. Hosted services run these bots for you and
hold your exchange keys. cuwan runs them on your own machine instead, and
you and an AI agent (Claude Code, Codex, Cursor, your own bot) can both
drive it:

- **Your keys stay in one encrypted file** under `~/.cuwan`, sealed with a
  key kept in the macOS keychain or the Linux Secret Service. An AI agent
  sees `[secret:NAME]`, never the value.
- **Look before you trade.** `cuwan run start NAME --dry-run` shows every
  order and sends nothing.
- **Live orders need a person's yes.** cuwan refuses a key that can
  withdraw, caps leverage at 20x, and closes a position before it nears
  liquidation. The close cannot be turned off.
- **Readable text for you, JSON for an agent** (`CUWAN_AGENT=1`).
- **One file to install**, no cloud runtime. cuwan is not a thin client
  to [efolusi.com](https://efolusi.com).

It is built like [komando](https://github.com/sgnd/komando): one static
binary for macOS and Linux, a sealed local store, and secrets referred to
as `secret:NAME`. Windows waits for a key store backend. The whole CLI is
about 4,600 lines of Go, small enough to read in an afternoon.

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

| Venue | Live |
|---|---|
| Binance spot and USD-M | yes |
| Hyperliquid perps | testnet only, until its testnet run is recorded |

## First commands

```bash
cuwan doctor                              # make ~/.cuwan and check the key store
printf '%s' "$API_KEY:$API_SECRET" | cuwan secret set binance-main
cuwan exchange add binance --venue binance --market usdm --secret binance-main
cuwan exchange test binance               # the key must not be able to withdraw
cuwan bot add btc-grid --exchange binance --symbol BTCUSDT \
  --lower 90000 --upper 110000 --levels 10 --order-size 0.001 --leverage 3
cuwan run start btc-grid --dry-run        # every order, nothing sent
cuwan run start btc-grid                  # asks you, then runs in the foreground
```

A live bot keeps its grid full: each filled order gets one counter order.
Stop the process and start it again, and `cuwan run start` resumes the same
run by client order ID. When the price leaves the range, `--on-exit pause`
(the default) waits, `stop` flattens and stops, and `trail` moves the range.
`--take-profit` and `--stop-loss` flatten and stop. `cuwan run stop NAME
--flatten` cancels the grid and closes the bot's own position.

## Safety

- Live orders need a person's yes; an AI agent cannot give it.
- cuwan refuses a key that can withdraw or transfer funds.
- Leverage is capped at 20x.
- The preventive close reads the venue's mark price and fires before the
  liquidation price: the venue's own, or a bound that assumes the largest
  maintenance margin. It runs before every other rule and cannot be turned
  off. Spot bots stop and sell back 5% below their range.

| Position | Entry | Venue liquidation | Leverage | Closes at mark |
|---|---|---|---|---|
| Long | 100 | 96 | 20x | 96.2 |
| Long | 100 | none given | 20x | 97.7 |
| Short | 100 | 104 | 20x | 103.8 |
| Short | 100 | none given | 10x | 104.75 |
| Grid long filled at 95 | 95 | none given | 20x | 92.815 |

The formula and every case are in [docs/design/guard.md](docs/design/guard.md).

## Agents

Set `CUWAN_AGENT=1`. Start with `cuwan guide` and `cuwan schema`.
`cuwan guide install` adds the skill for Claude Code, Codex, Cursor and
Gemini CLI. Secrets print
as `[hidden]` / `[secret:NAME]`. See [AGENTS.md](AGENTS.md) and
[skills/cuwan/SKILL.md](skills/cuwan/SKILL.md).

## License

[AGPL-3.0](LICENSE). Companies that need use without copyleft: see
[COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md). Contributors sign the
[CLA](CLA.md).

© Sugeng Agung Suganda
