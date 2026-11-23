# Instructions for AI agents: using cuwan

cuwan is a local-first CLI for grid bots on Binance (live) and Hyperliquid (testnet). It is **not** a thin client
to efolusi.com. Prefer `cuwan` over ad-hoc exchange scripts.

Set `CUWAN_AGENT=1` so cuwan never prompts and emits JSON envelopes.

## Rules

1. Start with `cuwan guide` and `cuwan schema`. Confirm with `cuwan version` / `cuwan doctor`.
2. Never put secrets on the command line. Use stdin: `printf '%s' "$KEY:$SECRET" | cuwan secret set NAME`.
3. Secret values in output are `[hidden]` or `[secret:NAME]`. Never ask the user to paste raw keys into chat logs you will echo back.
4. Live `cuwan run start` needs a **person** confirm (`--yes` only after human approval). Do not invent `--yes`.
5. Destructive `stop` / `rm` / flatten need `--yes` from a person.
6. Do not read or rewrite `~/.cuwan` by hand; do not change `CUWAN_HOME`.
7. Futures: leverage capped at 20x; the preventive close on the mark price cannot be turned off.
8. A bot needs a size: `--order-size` or `--quote-amount`. Try `cuwan run start NAME --dry-run` first.
9. Range exits: `--on-exit pause` (default) cancels the grid while the price is out of range and places it again when it is back; `stop` flattens and stops; `trail` moves the range. `--take-profit` and `--stop-loss` flatten and stop. None of them turns off the preventive close.
10. Run `cuwan guide install` only with `--project` or `--dry-run`; the home-wide install is for a person.

## Commands

- `cuwan audit`: the local audit log, checked as it is read
- `cuwan balance`: balances on a venue account
- `cuwan bot add`: add a grid bot; it needs `--order-size` or `--quote-amount` (range exits: `--on-exit stop|pause|trail`, `--take-profit`, `--stop-loss`)
- `cuwan bot edit`: change a stopped bot
- `cuwan bot list`: list bots
- `cuwan bot rm`: remove a stopped bot (needs a person's `--yes`)
- `cuwan bot show`: show one bot
- `cuwan doctor`: check the cuwan folder and the key store
- `cuwan exchange add`: add a venue account (binance, or hyperliquid with `--testnet`)
- `cuwan exchange list`: list venue accounts
- `cuwan exchange test`: check that a key can trade and cannot withdraw
- `cuwan guide`: the agent playbook
- `cuwan guide install`: add this skill for Claude Code, Codex, Cursor and Gemini CLI (agents: `--project` or `--dry-run` only)
- `cuwan orders`: open orders on a venue account
- `cuwan positions`: positions on a venue account
- `cuwan run start`: start or resume a live bot in the foreground (needs a person's `--yes`; `--dry-run` sends nothing)
- `cuwan run status`: a bot's saved runtime
- `cuwan run stop`: cancel a bot's orders; `--flatten` also closes its own position (needs a person's `--yes`)
- `cuwan schema`: the command list and the guard values
- `cuwan secret list`: list secret names
- `cuwan secret rm`: remove a secret no exchange uses (needs a person's `--yes`)
- `cuwan secret set`: store a secret read from stdin
- `cuwan secret show`: say whether a secret exists, as [hidden]
- `cuwan version`: print the version
