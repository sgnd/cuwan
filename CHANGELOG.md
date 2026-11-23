# Changelog

All notable changes are listed here, newest first. Each version starts with
its highlights; the sections below them give the details. Versions follow
[Semantic Versioning](https://semver.org), and the format follows
[Keep a Changelog](https://keepachangelog.com).

## v0.0.1 - 2026-11-22

Highlights: live grid bots from one local command on macOS and Linux, with
keys in one sealed file, live orders only after a person's yes, and a loop
that refills the grid and closes a position on the mark price before
liquidation. Binance runs live; Hyperliquid runs on its testnet.

### Added

- `cuwan secret`, `exchange`, `bot`, `run`, `orders`, `positions`, `balance`
  and `audit`: text for a person, JSON for an AI agent
  (`CUWAN_AGENT=1`), and `cuwan guide` and `cuwan schema` for agents.
- One sealed store file under `~/.cuwan`, bound to a generation that grows
  on every save, with a chained audit log. Its 256-bit key is generated and
  kept in the macOS keychain or the Linux Secret Service, never in a file.
  Secrets are read from stdin and print as `[hidden]`.
- `cuwan run start NAME` runs the live loop in the foreground: it confirms fills
  with the venue, places one counter order per filled grid order, and saves
  every order before it is sent so a timed-out send is found again by its
  client order ID.
- `cuwan run start` resumes a running or unguarded live bot after a restart:
  it keeps the run ID, finds every order again by its client order ID, and
  places no second grid. A halted bot still needs `cuwan run stop`.
- Range exits on `bot add` and `bot edit`: `--on-exit pause` (the default)
  cancels the grid while the price is out of range and places it again when
  the price is back; `--on-exit stop` flattens and stops; `--on-exit trail`
  moves the range by whole grid steps to the price. `--take-profit PRICE`
  and `--stop-loss PRICE` flatten and stop. The preventive close runs first
  on every step, so no exit setting can skip it.
- `cuwan guide install`: adds the skill to `~/.claude/skills` and
  `~/.agents/skills` (`--project` for the current folder, `--dry-run` to
  look first, a changed copy kept as `.bak`). An agent may run `--project`
  and `--dry-run`; the home-wide install answers `NEEDS_PERSON`.
- `cuwan run stop NAME [--flatten]`: cancels the bot's orders, and with
  `--flatten` closes the bot's own position with reduce-only IOC orders.
- Binance spot and USD-M: order status, symbol rules from `exchangeInfo`
  (tick, step, minimum size and value), and IOC orders.
- Hyperliquid perps: signed orders, cancels and leverage, order status, and
  symbol rules (5 significant figures, $10 minimum).
- `make testnet`: opt-in round trips on the Binance and Hyperliquid testnets.

### Security

- Live runs refuse a key that can withdraw. Binance keys are checked with
  `/sapi/v1/account/apiRestrictions`; a Hyperliquid key must be an API wallet
  of the configured account. A failed check refuses the run.
- Live runs need a person's `--yes`. Leverage is capped at 20x. The
  preventive close reads the mark price and fires before the liquidation
  price (the venue's own, or a bound that assumes the largest maintenance
  margin); see `docs/design/guard.md`. It cannot be turned off. Spot bots
  stop and sell back 5% below the range.
- A bot needs an explicit size (`--order-size` or `--quote-amount`).
- One lock per bot: `cuwan run stop` waits for the running loop to let go
  before it cancels anything.
- Live runs on a venue not marked `full` are refused unless the exchange is
  set to its testnet.
- Live client order IDs include a random ID per run.
- Exchange errors never hold an API key, secret, private key or signed
  query: drivers clean every HTTP error at the source.
- `golang.org/x/crypto` is v0.59.0.

### Removed

- Paper mode, backtest, report and the catalog of venues without a driver:
  the first release keeps what runs live.
- Windows builds, until cuwan has a Windows key store.
