# due

Every date that falls due, in one list, and actions fired at a date. One Go
binary: CLI, MCP server (`due mcp`) and terminal interface (`due tui`).

- **Ledger**: what no other tool carries (a contract's end, a warranty, a legal
  delay), one Markdown file each (`~/due/<sphere>/PE-0001.md`), versioned with jj.
  due never writes into reminders or calendars. An entry has a date, notices before it (`7d,1d`) and an
  action at the term: `tell`, `agent` or `command`.
- **Connectors**: dates read live from other tools, never copied: Apple
  Reminders and Calendar (`macos`), office waits, routine runs, oj sittings and
  actions, and any command printing JSON.
- **Launcher**: the launch agent `aero.clement.due` runs `due tick` every
  minute. Missed instants fire once, the latest only.
- **Spheres** (perso, pro) file things, they do not hide them: reads cover every
  sphere unless `--sphere` narrows them, and each line names its sphere. A write
  needs `--sphere`, chosen by what the entry is about; there is no default. Ids
  carry the sphere's prefix: `PE-0001` (perso), `UE-0001` (pro).

```bash
due init --sphere perso --root ~/due/perso
due add "Renouveler le passeport" --at 2026-12-01 --notice 30d,7d --sphere perso
due ls --until 7d --format text
due tui
due launcher install
due schema                     # every action, for agents
```

## Configuration

`~/.config/due/config.yaml`:

```yaml
default_time: "09:00"            # hour of a date alone and of its notices
spheres:
  perso:
    root: ~/due/perso
    prefix: P                    # ids PE-0001
    horizon: 30d
    actions:                     # placeholders: {id} {title} {message} {prompt} {dossier} {ref} {at} {when} {cwd}
      tell: ["office", "tell", "{dossier}", "--office", "~/offices/perso", "--text", "{message}"]
      agent: ["office", "notify", "desk", "{dossier}", "--office", "~/offices/perso", "--text", "{prompt}"]
    connectors:
      - {name: rappels, type: reminders, exclude_tags: [pro]}
      - {name: agenda, type: calendar, calendars: ["Agenda Privé"]}
      - {name: office, type: office, office: ~/offices/perso}
      - {name: routine, type: routine, owners: ["office:perso", "-"]}
      - {name: oj, type: oj, oj_sphere: perso}
      - {name: mnemo, type: command, run: ["my-dates", "--until", "{until}"]}
```

A `command` connector prints a JSON list of `{id, title, at, all_day, detail, ref}`,
bare, as `{"items": [...]}` or as `{"ok": true, "result": [...]}`.

## TUI

It opens on the ledger, at any date, grouped by period (late, this week, this
month…). Tabs: `s`/`S` or `1`–`9` move to everything (ledger and connectors
within the horizon, by day) and to each connector; `esc` comes back. The detail
follows the selection (right from 110 columns, below otherwise; `tab` hides it)
and shows each entry's timeline of notices and term. `a` and `e` open a form
that reads the date and notices as you type.

It updates itself: the files of the ledger, office, routine and oj are watched;
reminders and calendars are read again every minute.

`a` add · `e` edit (form) · `E` edit the file · `d` done/reopen · `z` snooze ·
`R` run now · `x` drop · `D` delete · `o` open (office dossier) · `f` done too ·
`H` horizon · `/` search · `r` read again · `?` help · `esc` back · `q` quit.
Mouse: click to select, wheel to scroll.

## License

MIT
