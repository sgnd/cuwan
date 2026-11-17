---
name: cuwan
description: Local-first CEX/DEX grid trading CLI. Use when running or configuring cuwan bots, secrets, Binance, or Hyperliquid from an agent.
---

# cuwan

## Setup

```bash
export CUWAN_AGENT=1
cuwan doctor
cuwan guide
cuwan schema
```

## Secrets

```bash
printf '%s' "$API_KEY:$API_SECRET" | cuwan secret set binance-main
# output: [secret:binance-main] = [hidden]
```

## Exchange + bot + paper

```bash
cuwan exchange add binance --venue binance --secret secret:binance-main --market spot
cuwan bot add btc-grid --exchange binance --symbol BTCUSDT --lower 90000 --upper 110000 --levels 11 --order-size 0.001
cuwan paper start btc-grid          # detaches a worker; sealed runtime under ~/.cuwan
cuwan paper status btc-grid         # worker_alive + last_price
cuwan paper stop btc-grid --yes     # signals worker via sealed status
# crash recovery: paper start again resumes open orders from sealed runtime
```

## Live (human required)

Do **not** pass `--yes` unless a person explicitly approved live trading in this session.

```bash
cuwan run start btc-grid --dry-run    # always allowed
cuwan run start btc-grid --yes        # only after human approval
cuwan run stop btc-grid --yes
```

## Guardrails

- Max leverage: 20x (clamped; no disable)
- Preventive reduce-only close: ~5% of path to estimated liquidation
