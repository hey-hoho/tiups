# tiup-backup

English | [简体中文](README_CN.md)

Copy the local `~/.tiup` (or `$TIUP_HOME`) to backup nodes so they can take over `tiup`.

```
tiup backup add -N 10.0.0.2,10.0.0.3 [-u tidb] [-P 22] [-i ~/.ssh/id_rsa] [--remote-dir DIR]
tiup backup remove -N 10.0.0.3
tiup backup list
tiup backup sync [-N 10.0.0.2] [--delete] [--dry-run] [--with-mirror] [--no-path] [--yes]
tiup backup job [-s "0 2 * * *"] [--disable]
tiup backup init-shell
```


> `sync` copies the per-cluster key tiup generated under `storage/cluster/clusters/<name>/ssh/`, but not
> keys you passed with `-i` from outside `~/.tiup`, nor keys held only in ssh-agent.

## Offline (local) mirror

If the source machine was set up from an offline package (`tiup mirror set /path/to/tidb-community-server-...`),
`tiup.toml` records that local directory, which lives outside `~/.tiup`. `sync` copies `tiup.toml` but not the
directory, so on the node every tiup command fails with

```
Error: stat /path/to/tidb-community-server-...: no such file or directory
```

`sync` prints a WARNING when it sees this. Two ways to fix it:

**Method 1: `--with-mirror` (copy the mirror to the same path on every node)**

```
tiup backup sync --with-mirror
```

The mirror directory is copied to the same absolute path on each node (the offline package is several GB, so
the first run takes a while; later runs are incremental). Run it once by hand: afterwards the scheduled job's
plain `sync` is enough. The node's user must be allowed to create that path. With `--delete`, extra files in
the node's copy of the mirror are removed too.

**Method 2: the node uses its own mirror (manual steps on the node)**

Every `sync` copies the whole `~/.tiup`, including `tiup.toml`, so a node's own mirror setting is overwritten
by the next sync. Use this method for a node you are taking over, or re-run step 3 after each sync.

1. Put the offline package on the node and unpack it, e.g.
   `tar xzf tidb-community-server-v8.5.4-linux-amd64.tar.gz -C /data`
2. Optional: the package ships `local_install.sh`, which installs tiup from the package and sets the mirror;
   skip it, tiup is already there.
3. Point tiup at the node's copy: `tiup mirror set /data/tidb-community-server-v8.5.4-linux-amd64`
4. Check with `tiup mirror show` and `tiup list`; neither should print `stat ...: no such file or directory`.

## How sync works

- **Auth**: with `-i` that key; otherwise ssh-agent, then `~/.ssh/id_ed25519|id_ecdsa|id_rsa`, then a password
  (prompted once and reused, or taken from `TIUP_META_PASSWORD`). Passwords are never stored.
  Passphrase-protected key files are not supported (use ssh-agent).
- **Host keys**: trust-on-first-use, kept in `$TIUP_HOME/storage/meta/known_hosts` (not synced). A changed key
  is always refused, even with `--yes`. In non-interactive runs an unknown host is refused unless `--yes` is given.
- **Transfer**: SFTP only (no rsync needed). Incremental by size + mtime, atomic replace, modes and symlinks
  kept (absolute symlinks into `$TIUP_HOME` are rewritten to the node's path).
- **PATH**: like the official installer, `sync` appends `export PATH=<dir>/bin:$PATH` (plus `TIUP_HOME` for a
  custom `--remote-dir`) to the node user's profile: the first existing of `~/.<shell>_profile`, `_login`,
  `rc`, else `~/.profile`, chosen from the node's `$SHELL`. It only appends a tagged block, never rewrites
  existing lines, and does nothing if `.tiup/bin` is already there. It takes effect in new login shells
  (`ssh` login, `bash -l`); non-login commands such as `ssh node tiup ...` do not read it. `--no-path` skips
  it; fish/csh are skipped with a notice.
- The whole `~/.tiup` is copied; there is no way to exclude parts of it. Only this tool's per-machine state is
  skipped: `storage/meta/known_hosts`, `sync.log`, `sync.lock`.
- `--delete` is opt-in (it removes files on the node that are not in the local `~/.tiup`).
  Backup nodes must have the same OS/arch (tiup binaries are copied as-is).
- The node list lives in `$TIUP_HOME/storage/meta/backup_nodes.json` and is synced too.
- `~/.tiup` contains cluster SSH private keys: use a trusted account on the nodes.

## Scheduled sync (`tiup backup job`)

- `tiup backup job` shows the schedule and the last log lines; `-s CRON` installs or replaces it in the
  current user's crontab (5 fields or `@daily` etc., each field range-checked); `--disable` removes it. Only the line tagged
  `# tiup-backup:sync` is ever touched.
- Cron cannot type passwords, so every node must accept key auth: set up passwordless SSH yourself
  (agent or default key, no `-i` needed) or use `tiup backup add -N ... -i KEY`. `job -s` connects to each
  node with keys only and refuses if any fails. Run `tiup backup sync` once by hand first so host keys are
  trusted.
- Output goes to `$TIUP_HOME/storage/meta/sync.log` (not synced, not rotated). A lock file stops two syncs
  (manual or cron) from running at once.
- Requires `crontab` and a running cron service (e.g. `dnf install cronie`).

## Build and package

```
make            # vet + test + build into dist/
make package    # dist/tiup-backup-linux-{amd64,arm64}.tar.gz (+ .sha256), each with the binary and install.sh
```

Without make: `go build -o dist/tiup-backup ./cmd/tiup-backup`
(cross-compile with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`).

To publish a release: run `make package`, create a GitHub release in
<https://github.com/hey-hoho/tiups> and attach the four files `tiup-backup-linux-{amd64,arm64}.tar.gz` and
`tiup-backup-linux-{amd64,arm64}.tar.gz.sha256` as its assets (the one-line installer needs both the package and
its `.sha256`). `install.sh` itself is served straight from the repository. To publish somewhere else, use
`make package BASE_URL=https://example.com/dir`, which bakes that download location into `dist/install.sh`.

## Install (`scripts/install.sh`)

### One line

On a machine that already has tiup (`$TIUP_HOME/bin` exists), the same way tiup itself is installed:

```
curl --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/hey-hoho/tiups/main/scripts/install.sh | sh
```

Options go after `sh -s --`, for example a specific release instead of the latest, or without touching your
shell profile:

```
curl --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/hey-hoho/tiups/main/scripts/install.sh | sh -s -- --version v0.1.0
curl --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/hey-hoho/tiups/main/scripts/install.sh | sh -s -- --no-shell
```

The script downloads `tiup-backup-<os>-<arch>.tar.gz` and its `.sha256` from the GitHub releases of this
project (`--version TAG` or `TIUP_BACKUP_VERSION` picks a release; `--base-url URL` or `TIUP_BACKUP_BASE_URL`
points it elsewhere), checks the checksum, and aborts without installing anything if it does not match, if the
checksum file is missing or if the URL is not https (`--no-verify` skips the check). Redirects to plain http
are refused. The whole script runs inside one function that is called on its last line, so a download cut off
halfway executes nothing. Only linux amd64/arm64 packages are published.

### From an unpacked release package

```
./install.sh && . ~/.bashrc             # or: ./install.sh -f tiup-backup-linux-amd64.tar.gz
./install.sh --tiup-home /data/tiup     # non-default TiUP home
./install.sh --no-shell                 # binary only, do not edit ~/.bashrc / ~/.zshrc
./install.sh --uninstall                # remove binary, shell function and the scheduled job
```

An installer runs in its own process and cannot change your current shell, so load the new function with
`. ~/.bashrc` (`. ~/.zshrc` for zsh) yourself; `&&` above does it in one line. Otherwise open a new shell.

It copies the binary to `$TIUP_HOME/bin/tiup-backup` (atomically, after checking that it runs on this
machine, so a bad build never replaces a working one) and adds a tagged `tiup()` function block to
`~/.bashrc` / `~/.zshrc`. Re-running upgrades the binary and leaves the shell block alone. `--uninstall`
restores the rc file and keeps the node list in `$TIUP_HOME/storage/meta`. `tiup backup sync` already copies
`bin/tiup-backup` to the backup nodes, but their shell function still needs `install.sh` (or
`tiup-backup init-shell >> ~/.bashrc`) once.

## Why a shell function (tiup refuses unsigned local components)

tiup only runs components listed in the signed index of its current mirror, so dropping a binary into
`~/.tiup/components/` is not enough. The shell function routes `tiup backup ...` to `tiup-backup` and
everything else to the real tiup. It only exists in interactive shells; scripts and cron must call
`tiup-backup` directly. The alternative is publishing the component to a private mirror:
`tiup mirror publish backup v0.1.0 <tar.gz> tiup-backup`.

## Project layout

```
cmd/tiup-backup/     program entry point
internal/cli/        cobra commands: add, remove, list, sync, job, init-shell
internal/config/     TIUP_HOME resolution, backup node list, state file paths
internal/remote/     SSH + SFTP connection: auth methods, host key checking
internal/syncer/     local scan, SFTP transfer, remote PATH setup
internal/cron/       crontab entry for the scheduled sync
internal/lock/       single-instance file lock for sync
internal/shell/      shell quoting and the init-shell snippet
scripts/install.sh   one-step installer for a tiup environment
Makefile             build, test and release packaging
```
