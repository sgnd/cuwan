# Security

cuwan holds exchange API keys, DEX wallet keys, and bot state under
`~/.cuwan`. A security bug can expose those secrets to an AI agent or
another user of the same machine.

## Report a problem

Please don't open a public issue for a security problem. Report it privately:

1. Open the [Security tab](https://github.com/sgnd/cuwan/security) of this repo.
2. Click **Report a vulnerability**.

Include what you found, how to reproduce it, and what an attacker could do.
You get an answer within a week. Once it is fixed, a new release says what
changed.

## What counts

- Reading a vault secret without a human approving it (JSON output, errors, logs).
- An AI agent seeing secret values that should stay `[hidden]` / `secret:NAME`.
- Tampering with `~/.cuwan/store.db` that goes unnoticed.
- Changing `CUWAN_HOME` for an agent without a person's yes.
- Live trading with a withdrawal-capable API key when the venue can detect it.

## Least-privilege keys

- Store every venue credential as `secret:NAME` via `cuwan secret set`,
  never on argv. Agent output shows `[hidden]` / `[secret:NAME]` only.
- **Binance (native live):** create an API key with spot and/or futures
  trading only. Disable withdrawals. Prefer IP allowlists.
- **Hyperliquid (native paper):** keep the trading wallet private key only
  in the vault. Prefer an API wallet with limited permissions when the
  venue supports it.
- **Other registered venues:** paper uses public tickers where wired; live
  trading stays off until a native/library adapter is enabled for that id.

## Local store

Home is mode `0700`. The sealed SQLite file is `0600`. Master key defaults
to the OS keychain; passphrase (`CUWAN_VAULT_PASS`) and `store.key` are
supported. Unlock env vars are stripped from the process so children never
inherit them. There is no sync daemon in the OSS CLI.

## Safety rules that hold for AI agents

- An AI agent cannot start, stop or remove anything: it gets NEEDS_PERSON.
  A person types the yes at their own terminal (`/dev/tty`); stdin is never
  read as the answer.
- On macOS the store key asks the person on every read. Never choose
  "Always Allow" on that keychain prompt: it would let cuwan, and anything
  that runs as cuwan, read the key without asking. On Linux the Secret
  Service hands it to any process of the user once the keyring is unlocked.
- Known limit: an agent that drives a real terminal (a pty) looks like a
  person to cuwan. Run agents without a terminal, or with `CUWAN_AGENT=1`.
