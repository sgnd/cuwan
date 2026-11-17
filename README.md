# cuwan

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![CI](https://github.com/sgnd/cuwan/actions/workflows/ci.yml/badge.svg)](https://github.com/sgnd/cuwan/actions/workflows/ci.yml)

**Local-first CLI for CEX/DEX grid bots.**  
For you and your AI agent.

cuwan runs grid strategies on your machine against Binance (spot + USDT-M)
and Hyperliquid (perps). State and secrets live under `~/.cuwan` (override:
`CUWAN_HOME`). No cloud runtime — this is not a thin client to
[cuwan.xyz](https://cuwan.xyz).

Sister architecture to [komando](https://github.com/sgnd/komando): static
binary (`CGO_ENABLED=0`), sealed local store, secrets as `secret:NAME`,
safe by default, text for humans and JSON for agents (`CUWAN_AGENT=1`).

## Quick start

```bash
make build
./bin/cuwan            # command list
./bin/cuwan version
./bin/cuwan doctor     # create ~/.cuwan, check sealed store

# Vault + exchange (keys never on argv in agent mode)
./bin/cuwan secret set binance-main
./bin/cuwan exchange add binance --secret secret:binance-main --market spot

# Paper grid (public market data; no signed orders)
./bin/cuwan bot add btc-grid --exchange binance --symbol BTCUSDT --lower 90000 --upper 110000 --levels 10
./bin/cuwan paper start btc-grid
./bin/cuwan paper status btc-grid
```

Live trading needs an explicit person confirm. Futures leverage is capped
at **20x**; a **~5%** preventive reduce-only close vs estimated liquidation
is always on (no disable flag). Binance live place/cancel is wired; Hyperliquid
live signing is not enabled yet (paper + public info work; use paper for HL).

## Install

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/sgnd/cuwan/main/install.sh | sh
```

Or build from source: `git clone https://github.com/sgnd/cuwan.git && cd cuwan && make build`.

## Agents

Set `CUWAN_AGENT=1`. Start with `cuwan guide` and `cuwan schema`. Secrets
print as `[hidden]` / `[secret:NAME]`. See [AGENTS.md](AGENTS.md) and
[skills/cuwan/SKILL.md](skills/cuwan/SKILL.md).

## License

[AGPL-3.0](LICENSE). Companies that need use without copyleft: see
[COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md). Contributors sign the
[CLA](CLA.md).

© Sugeng Agung Suganda
