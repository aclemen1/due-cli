---
name: due
description: Every date that falls due for the user, in one list, and actions fired at a date. Use when the user asks what is coming ("qu'est-ce qui arrive cette semaine ?", "mes échéances", "what's due"), wants to be told before a date ("rappelle-moi 7 jours avant"), or wants something done at a date (a prompt to an agent, a command). Lists the ledger of due and, live, reminders, calendars, office waits, routines and oj sittings.
---

# due

`due` keeps its own ledger of entries and reads, live, the dates of other tools.
Run `due schema` for the catalog, `due schema due <action>` for one action.

## Read

```bash
due ls --sphere perso --format text            # horizon of the sphere (30d)
due ls --until 7d --sphere perso
due ls --source due --all --sphere perso       # the ledger only, done included
due show E-0007 --sphere perso
```

- A line from a connector (`source` ≠ `due`) belongs to its tool: change it there (macos, office, routine, oj).
- `!` marks a late line. `errors` lists connectors that failed.

## Write (ledger only)

```bash
due add "<titre>" --at 2026-11-15 --notice 7d,1d --sphere perso
due add "<titre>" --at "2026-11-15 14:00" --do tell --body "<message>" --sphere perso
due add "<titre>" --at 2026-11-15 --do agent --cwd <dir> --body "<prompt>" --sphere perso
due add "<titre>" --at 2026-11-15 --do command --run '<shell>' --sphere perso
due edit 7 --at 2026-11-20 --sphere perso
due snooze 7 --by 7d --sphere perso
due done 7 --note "<résultat>" --sphere perso
```

| Field | Rule |
|---|---|
| `--at` | date alone fires at `default_time` (09:00) |
| `--notice` | each delay tells before the term, even without `--do` |
| `--do` | `tell` (message to the user), `agent` (body = prompt), `command` (`--run`) |
| `--ref` | what the entry belongs to, e.g. `office:P-0040` |

- An entry with `--do agent` or `--do command` runs without review at its date: create one only after the user's agreement.
- Spheres never mix: always pass `--sphere`.

## Firing

The launch agent `aero.clement.due` runs `due tick` every minute. `due launcher status|stop|start`.
Missed instants fire once, the latest only. `due run 7` fires by hand.
