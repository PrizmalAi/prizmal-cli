# AGENTS.md

## Design Principle: prefer ephemeral config; persisted edits are permanent until explicitly restored

`prizmal` configures a harness for a launch. The default mechanism is
**ephemeral**: pass the configuration through CLI arguments and environment
variables scoped to the launched process, so a plain launch does not need to
touch the harness's configuration files at all.

**Invariant — persisted edits are permanent:** when `prizmal` edits a
harness's configuration file, that edit persists after the harness exits.
Once the config is modified, it is modified. `prizmal` never reverts,
rolls back, or "cleans up" a config edit on exit — not automatically, not on
the next launch. The only thing that removes Prizmal from a harness's
configuration is the explicit restore command (`--restore`), which undoes a
previous edit from its backup (`fileutil.WriteWithBackup` writes one).

### The `--persist` opt-in

Users who want a persistent change may opt in with `--persist`, which writes
the integration's configuration without launching. This is the sanctioned
way to make an explicit, permanent edit outside a launch. Like every
persisted edit, it stays until `--restore` removes it.

### When a harness cannot be ephemeral

If a harness has no env-var or inline-config mechanism, write the config
file with the backup + `--restore` pattern and document the limitation in
the README's integration table. The edit is a real configuration change: it
persists, and the user removes it with `--restore`, never by re-running or
exiting `prizmal`.

## No ticket references in committed files

Never write a `PRI-NNNN` ticket reference in a committed file. Git already
links each line to the change that produced it, and the id is redundant on
finished content. `TestNoTicketRefsInCommittedFiles` fails on one, and
the pull-request body is where a ticket link belongs, because Linear's
GitHub integration reads it there to build its linkback comment.

## Harness workflows

Each harness has its own workflow, named after it (`claude-code.yml`,
`codex.yml`, `cline.yml`, `opencode.yml`, `pi.yml`), so the harnesses run
in parallel and each failure shows the harness's name. The `launch` job runs
the latest release of the harness through `prizmal` with a one-shot prompt,
against a stub switch (`internal/stubserver`) that answers every turn with
`stubserver.Reply`. It passes when the harness prints that reply on stdout. Nothing in CI
uses a Switch key or a live model.

`TestHarnessLaunchRegistryCompleteness` fails when an integration in the
launcher registry has no launch case, or when its workflow does not run the
case's test with `PRIZMAL_HARNESS_LAUNCH: require`. So a new harness
comes with a launch case and a workflow of its own.

## Terminal baselines

Each harness has a test that renders its screens in a tmux terminal of a
fixed size and compares their text, without colour, to the files in
`cmd/prizmal/testdata/terminal/<harness>`. The screens come from the real `prizmal`
binary and the real harness, run against a stub switch that serves
`GET /v1/models`. Each harness has its own workflow, so the harnesses run
in parallel under their own names.

`TestTerminalBaselinesClaude`, run by the Claude Code workflow, covers the
prizmal model picker and Claude Code's startup screen and `/model` picker.
It launches Claude Code through the picker and with a model passed as `-m`
or `--model`, with Claude Code installed at the version that
`cmd/prizmal/testdata/terminal/claude/claude-code-version` records.

When a pull request changes a baseline, its body must include a pixel
screenshot of every changed screen. The text diff lists the rows that moved,
and the image adds the colour, weight and glyphs a reviewer needs to judge
the change. The baseline-screenshot workflow fails a pull request that
changes a file under `cmd/prizmal/testdata/terminal` and has no image in its body.

Regenerate the baselines and save each screen with its colour codes:

```bash
mkdir -p /tmp/shots
PRIZMAL_TERMINAL_SHOTS=/tmp/shots go test -count=1 -run TestTerminalBaselinesClaude -update-baselines ./cmd/prizmal
```

Then draw each changed screen with termframe at the size in its file name,
and attach the PNG to the pull-request body:

```bash
termframe -W 100 -H 30 -o shot.svg < /tmp/shots/claude-startup-m-smart-100x30.ansi
rsvg-convert -z 3 -o shot.png shot.svg
```

A baseline keeps the Claude Code version it was recorded with, and the
comparison ignores the version in Claude Code's banner. To move to a new
release, write its version to `cmd/prizmal/testdata/terminal/claude/claude-code-version`
and install it. Then run the update: it rewrites only the screens that
changed for another reason, and those need screenshots like any other
baseline change. The Claude Code cases skip on a machine with another
Claude Code version installed.

Check the screenshot requirement before the pull request goes up, not
after CI fails. When a change touches a file under
`cmd/prizmal/testdata/terminal`, the body must already carry an image
link. A body edit re-runs the check (`edited` is in the workflow's
trigger list), but a red first cycle is noise a local pass avoids: diff
the change for `testdata/terminal`, and when it matches, run the update
command with `PRIZMAL_TERMINAL_SHOTS` set, render each changed screen
with termframe at the size in its file name, and embed the PNG with the
`attach-screenshots` skill before `gh pr create`.

The `cmd/prizmal` test binary defines the `-update-baselines` flag,
so run the update from `cmd/prizmal`, not from the repository root:

```bash
cd cmd/prizmal
env -u PRIZMAL_SWITCH_KEY -u PRIZMAL_SWITCH_URL PRIZMAL_TERMINAL_SHOTS=/tmp/shots \
  go test -count=1 -run 'TestTerminalBaselinesClaude/<case>' -update-baselines .
```

Without the `env -u`, a `PRIZMAL_SWITCH_KEY` or `PRIZMAL_SWITCH_URL` in
the launch environment leaks into tests whose assertions read that
environment and expect it to contain nothing, and two launcher tests
fail for an unrelated reason.
