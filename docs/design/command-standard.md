# Command standard

Grammar: `cuwan <noun> <verb> [flags] [args]`.

| Noun | Verbs |
|------|-------|
| secret | set, list, show, rename |
| exchange | add, list, test |
| bot | add, list, show, edit, rm |
| paper | start, status, stop |
| run | start, status, stop |
| orders / positions / balance | (read-only) |
| audit | (list local action log) |

Meta: `version`, `doctor`, `guide`, `schema`, `help`.

Exit codes: `0` ok, `1` error, `2` usage.
Agents get a JSON envelope on stdout when `CUWAN_AGENT=1` or stdout is not a TTY (see `internal/out`).
