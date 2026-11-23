# Security design

See also [SECURITY.md](../../SECURITY.md) and [guard.md](guard.md).

## Threat model

Someone who can read `~/.cuwan` but has no store key must not recover an API
key or a wallet key. A changed, moved or dropped part of the store must fail
to open, never decrypt to something else. An AI agent driving cuwan must not
be able to place a live order, remove data or switch off a guard without a
person's yes.

## Sealed store

- One file, `store.sealed`: an 8-byte generation, then the whole state
  sealed with AES-256-GCM (`internal/sealrow`, key from HKDF-SHA256). The
  generation is in the authenticated data and grows on every save, so a
  changed generation does not open.
- Audit entries are chained by SHA-256 and checked on every read.
- A lock file makes every read-change-write one step across processes.
  Writes go to a temporary file, are synced, then renamed into place.
- Home `0700`, store file `0600`.
- Not covered: someone who can write the folder may put back a whole older
  file; it opens at its older generation.

## Store key

- 32 random bytes from `crypto/rand`, made on first use and kept in the
  macOS keychain (`security -i` reads the command from stdin, so the key is
  never on a command line) or the Linux Secret Service (`secret-tool`).
- No passphrase mode and no key file. When the key store fails, cuwan stops.
- Windows has no backend yet, so there is no Windows build (CW26).

## Secrets

- Read from stdin only, never from argv. Output shows `[hidden]` and
  `[secret:NAME]`.
- Every driver error goes through `exchangex.CleanErr`: a failed request's
  URL keeps only scheme, host and path, and the key, the secret and any
  signed query are replaced. Tests use a closed server and an echo server
  with fake keys.

## Agents

- `run start`, `run stop`, `bot rm` and `secret rm` need a person's yes: a
  terminal answer, or `--yes`. An agent (`CUWAN_AGENT=1`, or no terminal)
  is never prompted, and the rules for agents say to pass `--yes` only after
  a person approved.
- A live run refuses a key that can withdraw or transfer, and refuses when
  the permission check fails.
- `guide install` writes into the home folder only for a person; an agent
  may use `--project` or `--dry-run`.

## Post-quantum

cuwan stores secrets (exchange keys and the store key) and moves them only
to the venue it trades on. Where each part stands:

| Part | Today | Post-quantum status |
|---|---|---|
| Store encryption | AES-256-GCM, key from HKDF-SHA256 | 256-bit symmetric key: holds |
| Store key | 32 random bytes from `crypto/rand`, in the system key store | 256-bit generated secret: holds |
| Venue transport | HTTPS from Go's `crypto/tls`, which offers X25519MLKEM768 by default | hybrid post-quantum key exchange when the venue's server supports it; cuwan cannot force it |
| Venue order signing | Binance: HMAC-SHA256 with the API secret. Hyperliquid: secp256k1 ECDSA | HMAC-SHA256 holds. secp256k1 has no post-quantum option; it waits on the venue (CW17) |
| Error text and logs | every driver error is cleaned of keys, secrets and signed queries | keeps secrets out of output a person or agent copies |

cuwan never asks a person to choose a key or a pairing secret. A secret
cuwan makes is 256 bits from `crypto/rand`.
