# Plan

The one list of planned work for the cuwan CLI. Mark an item `done` or
`deferred` (with the reason and what it waits on). Never delete an item.

The paid plan (`efolusi/cuwan-apps`, `efolusi/cuwan-cloud`) starts only after
milestone M5 is done.

## Milestones

| # | Milestone | Status |
|---|---|---|
| M1 | Safety and test harness | done |
| M2 | Command tests for every verb | open |
| M3 | Live engine loop | open |
| M4 | Venue drivers on testnets | open |
| M5 | Release readiness | open |

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
| Tests for `secret set/list/show/rename` (stdin only for agents, values hidden) | open |
| Tests for `exchange add/list/venues/test` | open |
| Tests for `bot add/list/show/edit/rm` and flag validation | open |
| Tests for `paper start/status/stop` with a fake public ticker | open |
| Tests for `orders`, `positions`, `balance`, `audit`, `doctor`, `guide`, `schema` | open |
| `cuwan schema` output checked against the real command list | open |

## M3: Live engine loop

`run start` today places the grid once and exits. Nothing watches fills.

| Item | Status |
|---|---|
| Worker that refills the opposite side when a grid order fills | open |
| ~5% preventive reduce-only close enforced while running (cannot be disabled) | open |
| `run stop --flatten`: real reduce-only close (today it only cancels) | open |
| Restart safety: resume from stored runtime by client order ID, no duplicate orders | open |
| Tests for the loop against the fake driver | open |

## M4: Venue drivers on testnets

| Item | Status |
|---|---|
| Binance spot and USD-M testnet: signed orders, permission probe | open |
| Hyperliquid testnet: L1 signing (today returns an error) | open |
| Opt-in integration tests behind an env flag, testnet keys only | open |
| Catalog venues stay `paper-only` or `stub` until a native driver lands | deferred: waits on M4 drivers above |

## M5: Release readiness

| Item | Status |
|---|---|
| `cuwan bot draft-range` (stub today) | deferred: product decision on the AI range draft; after M3 |
| Release workflow (GoReleaser, checksums), like komando | open |
| README and `docs/` updated for every command | open |
| `make security` (govulncheck) in CI | open |
