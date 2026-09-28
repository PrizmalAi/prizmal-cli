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