# Contributing

Thank you for helping. Please write issues, pull requests and docs in English.

cuwan is open source under the [AGPL-3.0](LICENSE), and is also sold under a
[commercial license](COMMERCIAL-LICENSE.md). Before your first pull request
is merged, please sign the [Contributor License Agreement](CLA.md): put its
sentence in the pull request description, and a check looks for it. You
keep the copyright of what you wrote.

cuwan is a local-first command line for CEX/DEX grid bots. It follows the
same architecture ideas as [komando](https://github.com/sgnd/komando): one
static binary, a sealed local store, secrets out of sight, safe by default,
for people and AI agents.

## Report a bug or suggest a feature

Open an issue on [github.com/sgnd/cuwan](https://github.com/sgnd/cuwan).
For a security problem, see [SECURITY.md](SECURITY.md) instead.

## Build and test

You need [Go](https://go.dev/dl/) (the version in [`go.mod`](go.mod) or newer),
`make` and `git`.

```bash
make build    # compile ./bin/cuwan
make test     # unit tests
```

`make help` lists everything.

## Before you add something

- It fits local trading / grid work, or the shared foundation (store, secrets, output).
- It works as a command for a person and for an AI agent: no prompt an agent can meet.
- Prefer no new dependency; if needed, keep it small and permissively licensed (MIT/BSD/Apache). Pure Go (`CGO_ENABLED=0`).
