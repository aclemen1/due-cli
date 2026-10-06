# due

Every date that falls due, in one list, and actions fired at a date. One Go
binary: CLI, MCP server (`due mcp`) and terminal interface (`due tui`).

- **Ledger**: what no other tool carries (a contract's end, a warranty, a legal
  delay), one Markdown file each (`~/due/<sphere>/E-0001.md`), versioned with jj.
  due never writes into reminders or calendars. An entry has a date, notices before it (`7d,1d`) and an
  action at the term: `tell`, `agent` or `command`.
- **Connectors**: dates read live from other tools, never copied: Apple
  Reminders and Calendar (`macos`), office waits, routine runs, oj sittings and
  actions, and any command printing JSON.
- **Launcher**: the launch agent `aero.clement.due` runs `due tick` every
  minute. Missed instants fire once, the latest only.
- **Spheres** (perso, pro) never mix: each has its ledger, connectors and actions.

```bash
due init --sphere perso --root ~/due/perso
due add "Renouveler le passeport" --at 2026-12-01 --notice 30d,7d --sphere perso
due ls --until 7d --sphere perso --format text
due tui --sphere perso
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

It opens on the ledger, at any date. `s` cycles to everything (ledger and
connectors within the horizon), then to each connector; `esc` comes back.

`↵` detail · `a` add · `d` done/reopen · `z` snooze · `e` edit the file ·
`x` drop · `R` run now · `f` done entries too · `/` search · `s` source ·
`h` horizon · `?` help · `esc` back · `q` quit.

## License

MIT
