---
name: due
description: Dates that must not be missed and call for no action by themselves (a contract or signature deadline, a notice period, a warranty end), their notices, and what fires at a date. Use when the user asks what is coming ("qu'est-ce qui arrive cette semaine ?", "mes échéances", "what's due"), wants to be told before a date ("rappelle-moi 7 jours avant"), or wants something done at a date. Something to do is a task: use task, not due. Lists the ledger of due and, live, calendars, office waits, routines, oj sittings, tasks and the dates of the memory.
---

# due

`due` keeps its own ledger of entries and reads, live, the dates of other tools.
Run `due schema` for the catalog, `due schema due <action>` for one action.

## Read

```bash
due ls --format text                          # every sphere, horizon 30d
due ls --until 7d --sphere pro                 # one sphere
due ls --source due --all                      # the ledgers only, done included
due show PE-0007                               # P: perso, U: pro
```

- A line from a connector (`source` ≠ `due`) belongs to its tool: change it there (macos, office, routine, oj, task: `task edit|done`).
- `!` marks a late line. `errors` lists connectors that failed.

## Write (ledger only)

| Goes to | What |
|---|---|
| `due` | a date not to miss, with no action by itself: contract or signature deadline, notice period, warranty end, renewal, legal delay |
| `task` | anything to do, the user's or followed with someone: `task add` |

A deadline that calls for an action (cancel before a date) is both: the date in due, the action in task, each citing the other (`due add … --ref task:<id>`, `task add … --ref due:<id>`).
A date already in a task, a calendar, office or a routine stays there: check with `due ls --search <mot>` before `due add`.
due never creates a reminder or a calendar event.

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
| `--via` | channel: `mail` (can wait), `tell` (Telegram, needs attention), `push` (Pushover, urgent); default by attention |

## Channels

| Message | Channel |
|---|---|
| notice 7 days ahead or more, Monday digest | `mail` |
| notice under 7 days, term of `--do tell`, critical alert | `tell` |
| critical line on its day or late, term of a critical entry | `push` |

A channel without a command in `actions`, or whose send fails, falls back to `tell`.

## Critical lines

`due assess` asks the judge (jev, else OpenJev) whether forgetting a line would have legal, financial or irreversible consequences.
`due ls --critical` keeps those lines. `due alert` sends their notices (judge.notice) and, on judge.digest's weekday, a digest.
The launcher runs `assess` at judge.nightly and `alert` at judge.morning. The judge sees title, date, source, detail and sphere; never a body.

- An entry with `--do agent` or `--do command` runs without review at its date: create one only after the user's agreement.
- Reads cover every sphere unless `--sphere` narrows them; each line names its sphere.
- Writes need `--sphere`, chosen by what the entry is about (pro for work, perso otherwise); never a default, not even `$DUE_SPHERE`.
- Ids carry their sphere: `PE-0007` (perso), `UE-0007` (pro); a bare `7` needs `--sphere`.

## Firing

The launch agent `aero.clement.due` runs `due tick` every minute. `due launcher status|stop|start`.
Missed instants fire once, the latest only. `due run PE-0007 --sphere perso` fires by hand.
