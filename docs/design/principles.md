# Design principles

1. **Local-first.** Runtime is this CLI on the user's machine. No dependency on `api.cuwan.xyz`.
2. **Secrets never leak.** Vault values are double-sealed; agent JSON shows `[hidden]` / `[secret:NAME]`.
3. **Safe by default.** Live place/cancel needs person confirm; destructive stop/flatten needs `--yes`. Futures: 20x hard cap + ~5% preventive close (no disable).
4. **Dual UX.** Humans may be prompted; agents (`CUWAN_AGENT=1`) never are — missing input is an error.
5. **Noun verb.** `cuwan <noun> <verb> …` (exchange add, bot list, paper start).
6. **Pure Go.** `CGO_ENABLED=0`, single static binary.
