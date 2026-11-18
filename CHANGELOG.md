# Changelog

All notable changes are listed here, newest first. Each version starts with
its highlights; the sections below them give the details. Versions follow
[Semantic Versioning](https://semver.org), and the format follows
[Keep a Changelog](https://keepachangelog.com).

## [Unreleased]

These changes will be the first release, 0.0.1.

Highlights: grid bots on paper and live from one local command, with keys in
an encrypted vault, live orders only after a person's yes, and a loop that
watches fills and closes a position before it nears liquidation. Binance runs
live; Hyperliquid runs on its testnet.

### Added

- `cuwan secret`, `exchange`, `bot`, `paper`, `run`, `orders`, `positions`,
  `balance` and `audit`: text for a person, JSON for an AI agent
  (`CUWAN_AGENT=1`), and `cuwan guide` and `cuwan schema` for agents.
- A sealed local store and vault under `~/.cuwan`. Secrets are referred to as
  `secret:NAME` and print as `[hidden]`.
- Paper mode on public prices. After a fill, the opposite order goes one grid
  level away.
- `cuwan run start NAME --foreground` runs the live loop: it confirms fills
  with the venue, places one counter order per filled grid order, and saves
  every order before it is sent so a timed-out send is found again by its
  client order ID.
- `cuwan run stop NAME [--flatten]`: cancels the bot's orders, and with
  `--flatten` closes the bot's own position with reduce-only IOC orders.
- Binance spot and USD-M: order status, symbol rules from `exchangeInfo`
  (tick, step, minimum size and value), and IOC orders.
- Hyperliquid perps: signed orders, cancels and leverage, order status, and
  symbol rules (5 significant figures, $10 minimum).
- `make testnet`: opt-in round trips on the Binance and Hyperliquid testnets.
- A registry of further venue ids for paper (catalog entries; no live use).
- `cuwan backtest NAME`: replays a saved bot over public Binance candles with
  paper's fill rules and the live guards, and reports fills, round trips,
  profit, fees, drawdown and any guard stop.
- `cuwan report [NAME] [--fee 0.1%]`: realised (FIFO) and unrealised profit,
  estimated fees, fills and round trips per bot, from the local store only.

### Security

- Live runs refuse a key that can withdraw. Binance keys are checked with
  `/sapi/v1/account/apiRestrictions`; a Hyperliquid key must be an API wallet
  of the configured account. A failed check refuses the run.
- Live runs need a person's `--yes`. Leverage is capped at 20x, and a
  reduce-only close fires about 5% before the estimated liquidation; it
  cannot be turned off. Spot bots stop 5% below the grid.
- One lock per bot: `cuwan run stop` waits for the running loop to let go
  before it cancels anything.
- Live runs on a venue not marked `full` are refused unless the exchange is
  set to its testnet.
- Live client order IDs include a random ID per run.
- `golang.org/x/crypto` is v0.59.0.
