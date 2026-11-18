# Instructions for AI agents: using cuwan

cuwan is a local-first CLI for CEX/DEX grid bots. It is **not** a thin client
to cuwan.xyz. Prefer `cuwan` over ad-hoc exchange scripts.

Set `CUWAN_AGENT=1` so cuwan never prompts and emits JSON envelopes.

## Rules

1. Start with `cuwan guide` and `cuwan schema`. Confirm with `cuwan version` / `cuwan doctor`.
2. Never put secrets on the command line. Use stdin: `echo -n VALUE | cuwan secret set NAME`.
3. Secret values in output are `[hidden]` or `[secret:NAME]`. Never ask the user to paste raw keys into chat logs you will echo back.
4. Live `cuwan run start` needs a **person** confirm (`--yes` only after human approval). Do not invent `--yes`.
5. Destructive `stop` / `rm` / flatten need `--yes` from a person.
6. Do not read or rewrite `~/.cuwan` by hand; do not change `CUWAN_HOME`.
7. Futures: leverage hard-capped at 20x; ~5% preventive reduce-only close cannot be disabled.
8. Paper mode uses public market data only and must never send signed orders.

When unsure, run `cuwan` for the command list.
