# Plan

The one list of planned work for the cuwan CLI. Mark an item `done` or
`deferred` (with the reason and what it waits on). Never delete an item.

The paid plan (`efolusi/cuwan-apps`, `efolusi/cuwan-cloud`) starts only after
milestone M5 is done.

## Product goal

cuwan brings the hosted grid bot (the premium feature of 3Commas, Pionex
and similar services) to the user's own machine, for people and AI agents.
It follows komando: open-source CLI first, then paid apps and a paid cloud.

| Value | What it means for cuwan | Where |
|---|---|---|
| Open source, for transparency | Every order decision is in the public CLI; no hidden engine | M1 to M5 |
| Self-hosted, for ownership | Bots, keys and history live in `~/.cuwan`; runs with no cuwan server | M3, M6 |
| Private, for protection | No telemetry; keys never leave the machine; cloud sync is end-to-end encrypted | M6, M8 |
| Secure, for trust | Sealed vault, trade-only keys, person confirm for live orders, risk caps | M1, M3, M6 |
| Cloud, for convenience | Optional paid sync, alerts and an always-on runner | M8 |
| Commercial, for sustainability | Paid apps and cloud; the CLI stays free | M7, M8 |

## Milestones

| # | Milestone | Status |
|---|---|---|
| M1 | Safety and test harness | done |
| M2 | Command tests for every verb | done |
| M3 | Live engine loop | open |
| M4 | Venue drivers on testnets | open |
| M5 | Release readiness | open |
| M6 | Killer features and trust | open |
| M7 | Paid apps (`efolusi/cuwan-apps`) | open: starts after M5 |
| M8 | Paid cloud (`efolusi/cuwan-cloud`) | open: starts after M5 |

## M1: Safety and test harness

| Item | Status |
|---|---|
| CLI test harness: `run(args)` entry and a fake exchange driver | done |
| `run start` fails closed when the key permission check errors | done |
| `run start` sets leverage only after the person confirmed | done |
| Tests: agent needs `--yes`, withdraw key refused, dry run places nothing, stop needs `--yes` | done |
| `make lint` checks gofmt; tree formatted | done |

## M2: Command tests for every verb

| Item | Status |
|---|---|
| Tests for `secret set/list/show/rename` (stdin only for agents, values hidden) | done |
| Tests for `exchange add/list/venues` | done |
| Test for `exchange test` | done |
| Tests for `bot add/list/show/edit/rm` and flag validation | done |
| `bot add/edit` reject a range the grid cannot build and negative sizes | done |
| Tests for `paper start/status/stop` with a fake public ticker | done |
| Paper engine: opposite order goes one grid level away (was the same price, so it refilled every tick) | done |
| Detached paper worker end-to-end test (spawns a process) | open |
| Tests for `orders`, `positions`, `balance`, `audit`, `doctor`, `guide`, `schema` | done |
| Read commands use the `openExchange` seam (fakeable in tests) | done |
| `cuwan schema` output checked against the real command list | done |

## M3: Live engine loop

`run start` placed the grid once and exited. Nothing watched fills.

| Item | Status |
|---|---|
| Worker that refills the opposite side when a grid order fills | done: `internal/live` Step, one grid level away; `run start --foreground` loop |
| Fills count only on driver proof (`OrderStatus`); else marked `unknown`, nothing placed | done |
| ~5% preventive reduce-only close enforced while running (cannot be disabled) | done: from the venue position, each step |
| `run stop --flatten`: real reduce-only close (today it only cancels) | done |
| `run stop` fails and leaves the bot running if a cancel fails (was ignored) | done |
| Loop never overwrites a `stopped` written by `run stop` | done (small window remains: needs a store compare-and-set) |
| Store compare-and-set for runtime writes | done another way: one OS lock per bot; `run stop` asks the loop to stop and waits for the lock |
| Review of PR #6, 1 (high): `run stop` raced the loop; orders placed after the last save were lost | done: per-bot lock, save before and after every order |
| Review of PR #6, 2 (high): order marked filled before the opposite order was sent; a timed-out send was untracked | done: saved as `pending` first, reconciled by client order ID |
| Review of PR #6, 3 (medium): step errors retried forever | done: 6 failed steps in a row halt the bot and cancel its orders; a failed protective close halts at once and alerts |
| Review of PR #6, 4 (medium): no driver could confirm fills; spot had no loss guard | done: live start refuses drivers without `OrderStatus`; spot stops (cancels, does not sell) 5% below the grid |
| Review of PR #6, 5 (medium): flatten closed the whole symbol position | done: closes the bot's own net size; refuses when the venue position does not cover it |
| Review of PR #6, low: partial fills, empty IDs, close price and close check | done: partial fills count; empty IDs never match; fresh price for the close; close must be filled |
| Local alerts beyond stderr for halts and guards | open: see M6 alerts |
| Re-review N1 (high): live start without the loop left the bot unguarded | done: live start needs `--foreground`; a signal marks the bot `unguarded` |
| Re-review N2: prices and sizes not rounded to the venue filters | done: drivers report tick, step, minimum size; live start refuses drivers that cannot |
| Re-review N3: not-found right after a timeout marked the order failed | done: 3 lookups over 2 minutes first; stop cancels pending orders by client ID |
| Re-review N4: start over a halted bot dropped the old runtime | done: start refuses until `run stop` |
| Re-review N5: the close was a GTC limit and could leave the position open | done: reduce-only IOC, 3 tries with fresh prices, venue position must shrink |
| Re-review low: stop file left after timeout; `unknown` never rechecked; not-found on cancel; late fills on stop | done |
| Re-review low: Binance hedge mode (`positionSide`) | done: hedge-mode accounts are refused |
| Binance: tick and step from exchangeInfo, IOC closes, -2011 on cancel as not found | done |
| Review 3 (medium): client order IDs repeated across runs | done: a random run ID is part of every live client order ID |
| Review 3 low: unknown orders skipped by CancelAll; SIGHUP not caught; lost counter orders | done: unknown is cancelled; SIGHUP marks the bot unguarded; every filled grid order gets one counter order, retried if it failed |
| Review 3 low: minimum order value (notional) not in the size rules | done: Binance NOTIONAL and MIN_NOTIONAL |
| Detached live worker (like paper), so the loop survives the terminal | open: until then live start needs `--foreground` |
| Restart safety: resume from stored runtime by client order ID, no duplicate orders | open |
| Tests for the loop against the fake driver | done |

## M4: Venue drivers on testnets

| Item | Status |
|---|---|
| Binance spot and USD-M testnet: signed orders, permission probe | open |
| `OrderStatus` in native drivers (without it the live loop never refills) | done for Binance and Hyperliquid (cloid is a hash of the cuwan client order ID) |
| Hyperliquid rules from meta: size step, price tick from 5 significant figures, $10 minimum | done |
| Hyperliquid key check said a wallet key cannot withdraw without checking | done: fails closed until the signer can prove an API wallet |
| Hyperliquid signing (secp256k1, keccak, msgpack): new dependencies | done: owner said yes; matches the official SDK's test vectors; waits on independent review |
| Hyperliquid live key check: the secret must be an API wallet of the configured account | done: proven with `userRole`; anything else fails closed |
| Hyperliquid testnet round trip (like `make testnet` for Binance) | open |
| Hyperliquid venue label `paper-only` changed to `full` | open: after the first testnet round trip |
| Binance order states normalised to open, filled, canceled (`NEW` was stored as `new`, which the loop skipped) | done |
| Binance key check via `/sapi/v1/account/apiRestrictions` (USD-M was never checked; spot read the wrong field) | done |
| Hyperliquid testnet: L1 signing (today returns an error) | done: see Hyperliquid signing below |
| Opt-in integration tests behind an env flag, testnet keys only | done for Binance: `make testnet` (skipped without testnet keys; forced to testnet hosts); first run waits on the owner's testnet keys |
| Catalog venues stay `paper-only` or `stub` until a native driver lands | deferred: waits on M4 drivers above |

## M5: Release readiness

| Item | Status |
|---|---|
| `cuwan bot draft-range` (stub today) | deferred: product decision on the AI range draft; after M3 |
| Release workflow (GoReleaser, checksums), like komando | open |
| README and `docs/` updated for every command | open |
| `make security` (govulncheck) in CI | open |
| Go toolchain bump in `go.mod` once a release fixes GO-2026-6859 (`net/http`) | open |

## M6: Killer features and trust

| Item | Status |
|---|---|
| Backtest a grid on public historical candles before running it | open |
| Paper vs live profit report (realised, unrealised, fees) per bot | open |
| Local alerts on fill, stop, and preventive close (desktop notify, webhook the user sets) | open |
| Agent access: MCP server or skill in `skills/` covering every command, with the same guardrails | open |
| No telemetry: state it in README and `docs/design/principles.md`; a test that the CLI makes no call to a cuwan host | open |
| Export and import of bots and history (no secrets) for backup and moving machines | open |
| Security review of vault, live run and drivers (security-audit skill) | open |

## M7: Paid apps (`efolusi/cuwan-apps`)

Follows how `efolusi/komando-apps` is built. Starts after M5.

| Item | Status |
|---|---|
| Mac, iPhone and iPad app that drives the local CLI: bots, paper, live status, alerts | deferred: waits on M5 |

## M8: Paid cloud (`efolusi/cuwan-cloud`)

Follows how `efolusi/komando-cloud` is built. Starts after M5.

| Item | Status |
|---|---|
| Account, billing and website | deferred: waits on M5 |
| End-to-end encrypted sync of bots and history (no exchange keys in the cloud) | deferred: waits on M5 |
| Optional always-on runner and push alerts | deferred: waits on M5; needs a design for keys that never leave the user's control |
