# Security design

See also [SECURITY.md](../../SECURITY.md).

## Threat model

An attacker with filesystem read of `~/.cuwan` without the master key must
not recover API keys or wallet keys. Tampering with sealed rows must fail
open (wrong key / ErrOpen), not silently decrypt wrong plaintext.

## Sealed store

- Per-row AES-256-GCM; AAD binds id, type, version.
- HKDF labels namespaced `cuwan … v1` (store rows vs vault entries).
- Master key: OS keychain (default), passphrase, or `store.key` (mode 0600; refuse world-readable).
- Home `0700`; DB file `0600`; `secure_delete` on SQLite.

## Vault

Secrets stored as type `secret` records; value sealed again under vault HKDF
label bound to the secret name. CLI and agent output never print plaintext.
