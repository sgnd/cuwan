---
name: cuwan
description: Local grid trading CLI for crypto exchanges (live on Binance; Hyperliquid on testnet). Use when running or configuring cuwan bots, secrets, or exchanges from an agent.
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

## Exchange and bot

```bash
cuwan exchange add binance --venue binance --market usdm --secret binance-main --testnet
cuwan exchange test binance               # can trade, cannot withdraw
cuwan bot add btc-grid --exchange binance --symbol BTCUSDT --lower 90000 --upper 110000 --levels 11 --order-size 0.001 --leverage 3
cuwan run start btc-grid --dry-run        # always allowed; sends nothing
```

## Live (a person's yes)

An agent cannot start, stop or remove anything: cuwan answers NEEDS_PERSON with
the command. Show that command to the person; they run it at their terminal and
type the yes there.

```bash
cuwan run start btc-grid                  # the person runs it; it asks them at the terminal
cuwan bot edit btc-grid --on-exit trail --take-profit 120000 --stop-loss 85000   # only while stopped
cuwan run stop btc-grid --flatten         # the person runs it; closes the bot's own position
```

## Commands

- `cuwan audit`: the local audit log, checked as it is read
- `cuwan balance`: balances on a venue account
- `cuwan bot add`: add a grid bot; it needs `--order-size` or `--quote-amount` (range exits: `--on-exit stop|pause|trail`, `--take-profit`, `--stop-loss`)
- `cuwan bot edit`: change a stopped bot
- `cuwan bot list`: list bots
- `cuwan bot rm`: remove a stopped bot (needs a person at the terminal; an agent gets NEEDS_PERSON)
- `cuwan bot show`: show one bot
- `cuwan doctor`: check the cuwan folder and the key store
- `cuwan exchange add`: add a venue account with `--live` or `--testnet` (Hyperliquid runs on testnet only)
- `cuwan exchange list`: list venue accounts
- `cuwan exchange test`: check that a key can trade and cannot withdraw
- `cuwan guide`: the agent playbook
- `cuwan guide install`: add this skill for Claude Code, Codex, Cursor and Gemini CLI (agents: `--project` or `--dry-run` only)
- `cuwan orders`: open orders on a venue account
- `cuwan positions`: positions on a venue account
- `cuwan run start`: start or resume a live bot in the foreground (needs a person at the terminal; an agent gets NEEDS_PERSON; `--dry-run` sends nothing)
- `cuwan run status`: a bot's saved runtime
- `cuwan run stop`: cancel a bot's orders; `--flatten` also closes its own position (needs a person at the terminal; an agent gets NEEDS_PERSON)
- `cuwan schema`: the command list and the guard values
- `cuwan secret list`: list secret names
- `cuwan secret rm`: remove a secret no exchange uses (needs a person at the terminal; an agent gets NEEDS_PERSON)
- `cuwan secret set`: store a secret read from stdin
- `cuwan secret show`: say whether a secret exists, as [hidden]
- `cuwan version`: print the version

## Guardrails

- Max leverage: 20x (clamped; no disable)
- Preventive close on the mark price, before the liquidation price (docs/design/guard.md); no switch
- Secrets never appear as raw values in agent JSON (`[hidden]` / `[secret:NAME]`)
