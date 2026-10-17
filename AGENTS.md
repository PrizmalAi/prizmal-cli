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
that harness's doc. The edit is a real configuration change: it persists,
and the user removes it with `--restore`, never by re-running or exiting
`prizmal`.

## Design principle: the CLI is optional

A person can use the Switch from each harness without installing `prizmal`.
Each harness has a doc under `docs/`, named after it (`docs/claude-code.md`,
`docs/codex.md`, `docs/cline.md`, `docs/opencode.md`, `docs/pi.md`). The doc
records the environment variables, settings and model spellings that
reproduce what a `prizmal` launch configures, and the README's integration
table links to it. The interactive parts are the exception: the model
picker, the sign-in menu and the harness install prompt have no manual
equivalent.

When a change alters what a launch configures, such as an environment
variable, a settings field or a model name's spelling, the same pull request
updates the harness's doc under `docs/`. When the manual setup cannot match
the launch, the doc says what the launch adds.

The README and the harness docs are read by people, not by agents, so they stay
short. The README keeps a usage section per harness and a link to that
harness's doc. A harness doc covers usage and what differs from the harness run
outside Prizmal, on the assumption a reader already knows how the harness
works. Internal detail stays out of both: catalog fields, request and response
shapes, mechanism, and version trivia belong in the code that owns them.

## What a pull request body must carry

A reviewer has not read the ticket and will not open it. The body stands on its
own, and it opens with the main point: what the change does, and why it matters.
The mechanism comes after, under its own heading.

Every body carries four things, and says one line when a change has nothing to
draw rather than leaving the reader to guess:

- **Steps to reproduce.** The commands that show the old behavior and the new
  one, with values you invented. A defect fix also names the mechanism, the
  change that introduced it, and the guard it adds.
- **A before and after.** The wire request, the stored row, the config stanza or
  the rendered screen, trimmed to the fields that matter, in a fenced block.
  Never a capture from a real run: invent every tenant, key and amount.
- **A mermaid diagram** of the one mechanism the change turns on, eight nodes at
  most. When a request crosses more than one component, draw the hops.
- **The gates that passed**, and for each one that could not run here, say so in
  those words with the scope you assumed.

A choice you made yourself goes in an *Assumptions* section, so a reviewer can
overturn it with a comment. A green check on a stacked pull request is weak
evidence, and the body says so when the layer below is what the gates ran on.

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

## Keep a harness pinned to its latest release

Bump a harness to its latest release whenever you notice a newer one, in the
baselines pin and in what the docs claim. Pin and use an older release only
when a bug does not reproduce on the latest, and say why in the pull request
that pins it. The pinned version is
`cmd/prizmal/testdata/terminal/<harness>/<harness>-version`, and the baselines
beside it are recorded with it.

## Terminal baselines

Every screen an operator can see has a baseline: a capture rendered in a tmux
terminal of a fixed size and compared, without colour, to the text files under
`cmd/prizmal/testdata/terminal/<group>`. The captures come from the real
`prizmal` binary, run against a stub switch. Harness screens and prizmal's own
screens are separate groups, and each harness has its own workflow so the
harnesses run in parallel under their own names.

`TestTerminalBaselinesClaude`, run by the Claude Code workflow, covers the
prizmal model picker and Claude Code's startup screen, `/model` picker and
`/usage` screen. It launches Claude Code through the picker and with a model
passed as `-m` or `--model`, with Claude Code installed at the version that
`cmd/prizmal/testdata/terminal/claude/claude-code-version` records.
`TestClaudeTiersStartInAutoMode` runs in the same job on the same version. It
launches a model of each tier and fails when its session starts outside auto
mode, so a Claude Code release that changes which models get auto mode fails
when the pinned version moves.

`TestTerminalBaselinesCodex`, run by the Codex workflow, covers Codex's
startup screen, `/model` picker and `/status` screen, and a `codex exec` run.
It launches Codex with `-m`, with Codex installed at the version that
`cmd/prizmal/testdata/terminal/codex/codex-version` records. To add a screen,
add a case to `codexBaselineCases` and regenerate with `-run
TestTerminalBaselinesCodex`. Codex picks a random greeting and prints a temp
working directory and a session id, so the comparison replaces each with a
placeholder.

`TestTerminalBaselinesPrizmal` covers the screens prizmal draws for its own
commands: the first-run prompt, the device-login flow and the update check, under
`cmd/prizmal/testdata/terminal/prizmal`. It needs tmux but no harness, and the
Claude Code workflow runs it beside the Claude cases.

A change that affects what an operator sees on any screen must come with a
baseline for that screen, and its pull-request body must include a pixel
screenshot of every screen its baselines add or change. This holds for a screen
that is not yet in the baselines as much as for one that is: add a new screen to
the baselines in the same change, and update a changed screen's. The text diff
lists the rows that moved, and the image adds the colour, weight and glyphs a
reviewer needs to judge the change. The baseline-screenshot
workflow fails a pull request that changes a file under
`cmd/prizmal/testdata/terminal` and has no image in its body. A screen a change
affects but never captures has no file for that workflow to see, so the review
is what catches it. When in doubt, add the baseline.

Regenerate the baselines and save each screen with its colour codes.
Run from `cmd/prizmal`: the test binary there defines the
`-update-baselines` flag, and a run from the repository root fails with
`flag provided but not defined`. Without the `env -u`, a
`PRIZMAL_SWITCH_KEY` or `PRIZMAL_SWITCH_URL` in the launch environment
leaks into tests whose assertions read that environment and expect it
to contain nothing, and two launcher tests fail for an unrelated
reason. The shots directory comes from `mktemp -d`, so it is fresh per
run and concurrent agents never share it:

```bash
shots=$(mktemp -d)
cd cmd/prizmal
env -u PRIZMAL_SWITCH_KEY -u PRIZMAL_SWITCH_URL PRIZMAL_TERMINAL_SHOTS=$shots \
  go test -count=1 -run 'TestTerminalBaselinesClaude|TestTerminalBaselinesPrizmal' -update-baselines .
```

Then draw each changed screen with termframe at the size in its file name,
and attach the PNG to the pull-request body:

```bash
termframe -W 100 -H 30 -o "$shots/shot.svg" < "$shots/claude-startup-m-smart-100x30.ansi"
rsvg-convert -z 3 -o "$shots/shot.png" "$shots/shot.svg"
```

A baseline keeps the Claude Code version it was recorded with, and the
comparison ignores the version in Claude Code's banner. To move to a new
release, write its version to `cmd/prizmal/testdata/terminal/claude/claude-code-version`
and install it. Then run the update: it rewrites only the screens that
changed for another reason, and those need screenshots like any other
baseline change. The Claude Code cases skip on a machine with another
Claude Code version installed.

Check the screenshot requirement before the pull request goes up, not
after CI fails. When a change touches what an operator sees, the body must
already carry an image link for every screen it adds or changes. A body edit
re-runs the check (`edited` is in the workflow's trigger list), but a red
first cycle is noise a local pass avoids: name the screens the change affects,
run the update command above with `PRIZMAL_TERMINAL_SHOTS` set, render each
new or changed screen with termframe at the size in its file name, and embed
the PNG with the `attach-screenshots` skill before `gh pr create`. One baseline
update at a time per clone: two concurrent runs that share a shots
directory overwrite each other's captures, and a `mktemp -d` directory
per run is the default that keeps this out of the way.

Without the `env -u`, a `PRIZMAL_SWITCH_KEY` or `PRIZMAL_SWITCH_URL` in
the launch environment leaks into tests whose assertions read that
environment and expect it to contain nothing, and two launcher tests
fail for an unrelated reason.
