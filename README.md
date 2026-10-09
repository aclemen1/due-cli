# due

Every date that falls due, in one list, and actions fired at a date. One Go
binary: CLI, MCP server (`due mcp`) and terminal interface (`due tui`).

- **Ledger**: dates not to miss that call for no action by themselves (a
  contract or signature deadline, a notice period, a warranty end; anything to
  do is a task, in `task`), one Markdown file each (`~/due/<sphere>/PE-0001.md`), versioned with jj.
  due never writes into reminders or calendars. An entry has a date, notices before it (`7d,1d`) and an
  action at the term: `tell`, `agent` or `command`.
- **Connectors**: dates read live from other tools, never copied: Apple
  Reminders and Calendar (`macos`), office waits, routine runs, oj sittings and
  actions, dated tasks of `task`, and any command printing JSON.
- **Launcher**: the launch agent `aero.clement.due` runs `due tick` every
  minute. Missed instants fire once, the latest only.
- **Spheres** (perso, pro) file things, they do not hide them: reads cover every
  sphere unless `--sphere` narrows them, and each line names its sphere. A write
  needs `--sphere`, chosen by what the entry is about; there is no default. Ids
  carry the sphere's prefix: `PE-0001` (perso), `UE-0001` (pro).

```bash
due init --sphere perso --root ~/due/perso
due add "Expiration du passeport" --at 2026-12-01 --notice 30d,7d --sphere perso
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
      mail: ["office", "tell", "{dossier}", "--office", "~/offices/perso", "--source", "mail", "--text", "{message}"]      # can wait
      tell: ["office", "tell", "{dossier}", "--office", "~/offices/perso", "--text", "{message}"]
      push: ["office", "tell", "{dossier}", "--office", "~/offices/perso", "--source", "pushover", "--text", "{message}"] # urgent
      agent: ["office", "notify", "desk", "{dossier}", "--office", "~/offices/perso", "--text", "{prompt}"]
    connectors:
      - {name: rappels, type: reminders, exclude_tags: [pro], exclude_lists: [task]}
      - {name: agenda, type: calendar, calendars: ["Agenda Privé"]}
      - {name: office, type: office, office: ~/offices/perso}
      - {name: routine, type: routine, owners: ["office:perso", "-"]}
      - {name: oj, type: oj, oj_sphere: perso}
      - {name: task, type: task, task_sphere: perso}  # dated tasks; oj: sittings_only once oj hands its actions to task
      - {name: mnemo, type: command, run: ["my-dates", "--until", "{until}"]}
```

A `command` connector prints a JSON list of `{id, title, at, all_day, detail, ref}`,
bare, as `{"items": [...]}` or as `{"ok": true, "result": [...]}`.

## TUI

It shows a table (values equal to the line above left blank) and opens on the ledger, at any date, by period (late, this week, this
month…). Tabs: `s`/`S` or `1`–`9` move to everything (ledger and connectors
within the horizon, by day) and to each connector; `esc` comes back. The detail
follows the selection (right from 110 columns, below otherwise; `tab` hides it)
and shows each entry's timeline of notices and term. `a` and `e` open a form
that reads the date and notices as you type.

It updates itself: the files of the ledger, office, routine, oj and task are
watched; reminders and calendars follow `macos watch` (read again every minute
when it is not running).

Keys follow the ecosystem convention: `j k` · `gg G` · `[ ]` groups · `enter l` open ·
`esc h` back · `1`–`9` views · `s` sphere · `t T` sort · `/` filter · `!` critical ·
`c` new · `E` edit · `N` notes (end of file) · `e` close · `space` done/reopen ·
`z` snooze · `R` run now · `x` drop · `#` delete · `o` open · `f` done too ·
`H` horizon · `tab` detail · `r` read again · `?` help · `q` quit.
Mouse: click to select, wheel to scroll.

## License

MIT
