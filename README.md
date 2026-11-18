# cuwan

[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![CI](https://github.com/sgnd/cuwan/actions/workflows/ci.yml/badge.svg)](https://github.com/sgnd/cuwan/actions/workflows/ci.yml)

Local-first grid trading CLI for CEX/DEX (Binance + Hyperliquid). Sealed vault, agent-safe JSON.

cuwan runs grid strategies on your machine. State and secrets live under
`~/.cuwan` (override with `CUWAN_HOME`). No cloud runtime — this is not a thin
client to [cuwan.xyz](https://cuwan.xyz).

Sister architecture to [komando](https://github.com/sgnd/komando): static binary
(`CGO_ENABLED=0`), sealed local store, secrets as `secret:NAME`, safe by
default, text for humans and JSON for agents (`CUWAN_AGENT=1`).

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

## First commands

```bash
cuwan doctor                              # create ~/.cuwan, check sealed store
cuwan secret set binance-main             # keys never on argv in agent mode
cuwan exchange add binance --secret secret:binance-main --market spot

cuwan bot add btc-grid --exchange binance --symbol BTCUSDT \
  --lower 90000 --upper 110000 --levels 10
cuwan paper start btc-grid                # public market data; no signed orders
cuwan paper status btc-grid
```

Live trading needs an explicit person confirm. Futures leverage is capped at
**20x**; a **~5%** preventive reduce-only close vs estimated liquidation is
always on. Binance live place/cancel is wired; Hyperliquid live signing is not
enabled yet (paper + public info work).

## Agents

Set `CUWAN_AGENT=1`. Start with `cuwan guide` and `cuwan schema`. Secrets print
as `[hidden]` / `[secret:NAME]`. See [AGENTS.md](AGENTS.md) and
[skills/cuwan/SKILL.md](skills/cuwan/SKILL.md).

## License

[AGPL-3.0](LICENSE). Companies that need use without copyleft: see
[COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md). Contributors sign the
[CLA](CLA.md).

© Sugeng Agung Suganda
