#!/bin/sh
# Install tiup-backup into a TiUP environment so that `tiup backup ...` works.
#
# One line (downloads the release package from GitHub, verifies its sha256, installs it):
#   curl --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/hey-hoho/tiups/main/scripts/install.sh | sh
#   ... | sh -s -- --no-shell            # pass options
#   ... | sh -s -- --version v0.1.0      # a specific release instead of the latest
#
# From an unpacked release package or a local file:
#   ./install.sh                      # binary found next to this script
#   ./install.sh -f tiup-backup.tar.gz
#   ./install.sh --uninstall
#
# What it does:
#   1. copies the binary to $TIUP_HOME/bin/tiup-backup (atomic replace, safe for upgrades)
#   2. adds a small `tiup()` shell function to ~/.bashrc or ~/.zshrc that routes
#      `tiup backup ...` to tiup-backup (tiup only runs components from its signed mirror)
# Safe to run repeatedly.
#
# The whole script runs inside main(), invoked on the last line, so a download that is
# cut off halfway executes nothing.

set -eu

# Where release packages are downloaded from: <base>/tiup-backup-<os>-<arch>.tar.gz and
# <same>.sha256 (the assets of a GitHub release). Can be overridden with --base-url or
# $TIUP_BACKUP_BASE_URL; `make package BASE_URL=...` bakes another default into dist/install.sh.
DEFAULT_BASE_URL="https://github.com/hey-hoho/tiups/releases/latest/download"

BEGIN_MARK='# >>> tiup-backup >>>'
END_MARK='# <<< tiup-backup <<<'

die()  { echo "error: $*" >&2; exit 1; }
info() { echo "$*"; }
warn() { echo "warning: $*" >&2; }

usage() {
    cat <<'EOF'
Usage: install.sh [options]

  -f, --file PATH     binary or .tar.gz/.tgz package to install
                      (default: tiup-backup or tiup-backup-<os>-<arch> next to this script,
                       otherwise the release package is downloaded)
      --version TAG   install this release (e.g. v0.1.0) instead of the latest;
                      default: $TIUP_BACKUP_VERSION
      --base-url URL  where to download the release package from (https only)
                      (default: $TIUP_BACKUP_BASE_URL, else the GitHub releases of this project)
      --no-verify     do not check the downloaded package against its .sha256 file
      --tiup-home DIR TiUP home (default: $TIUP_HOME or ~/.tiup)
      --no-shell      do not touch ~/.bashrc / ~/.zshrc
      --uninstall     remove the binary, the shell function and the scheduled job
  -h, --help          show this help
EOF
}

# download URL DEST: https only (plain http is accepted for loopback, for testing).
download() {
    case "$1" in
        https://*)
            # --proto-redir: GitHub answers with redirects; never follow one to plain http
            curl_sec="--proto =https --proto-redir =https --tlsv1.2"; wget_sec="--https-only --secure-protocol=TLSv1_2" ;;
        http://127.0.0.1[:/]*|http://localhost[:/]*)
            curl_sec=""; wget_sec="" ;;
        *) die "refusing to download over an insecure URL: $1 (https is required)" ;;
    esac
    command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || die "need curl or wget to download $1"

    # GitHub is flaky on some networks (connection refused/reset, HTTP/2 stream errors, 5xx) and
    # curl's own --retry skips most of those, so retry here. A definite "no" (404, 403, ...) is
    # not retried. TIUP_BACKUP_RETRIES sets the number of attempts.
    tries=${TIUP_BACKUP_RETRIES:-8}
    case "$tries" in ""|*[!0-9]*) tries=8 ;; esac
    [ "$tries" -ge 1 ] || tries=1
    n=1
    while :; do
        if command -v curl >/dev/null 2>&1; then
            # shellcheck disable=SC2086
            # --speed-limit/--speed-time: give up on a stalled transfer (<1 KB/s for 30 s) instead of hanging
            code=$(curl $curl_sec -sSL --connect-timeout 15 --speed-limit 1024 --speed-time 30 \
                -o "$2" -w '%{http_code}' "$1" 2>/dev/null) && rc=0 || rc=$?
            if [ "$rc" -eq 0 ] && [ "$code" = "200" ]; then return 0; fi
            if [ "$rc" -eq 0 ]; then
                case "$code" in
                    400|401|403|404|410|451) warn "HTTP $code for $1"; return 1 ;;
                esac
            fi
            why="curl exit $rc, HTTP ${code:-none}"
        else
            # shellcheck disable=SC2086
            if wget -q -T 30 $wget_sec -O "$2" "$1" 2>/dev/null; then return 0; fi
            why="wget failed"
        fi
        [ "$n" -lt "$tries" ] || { warn "giving up on $1 after $n attempt(s) ($why)"; return 1; }
        warn "download failed ($why), retrying ($n/$tries)..."
        n=$((n + 1))
        sleep 2
    done
}

# check_sha256 FILE SUMFILE: SUMFILE holds "<64 hex>  <name>" as written by sha256sum.
check_sha256() {
    want=$(awk 'NR==1 {print tolower($1)}' "$2")
    case "$want" in
        *[!0-9a-f]*|"") die "malformed checksum file for $(basename "$1")" ;;
    esac
    [ "${#want}" -eq 64 ] || die "malformed checksum file for $(basename "$1")"
    if command -v sha256sum >/dev/null 2>&1; then
        got=$(sha256sum "$1" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        got=$(shasum -a 256 "$1" | awk '{print $1}')
    else
        die "no sha256sum/shasum to verify the download; install one or pass --no-verify"
    fi
    [ "$got" = "$want" ] || die "checksum mismatch for $(basename "$1"): expected $want, got $got; nothing was installed"
}

main() {
    SRC=""
    NO_SHELL=0
    UNINSTALL=0
    VERIFY=1
    BASE_URL="${TIUP_BACKUP_BASE_URL:-$DEFAULT_BASE_URL}"
    VERSION="${TIUP_BACKUP_VERSION:-}"
    TIUP_HOME_DIR="${TIUP_HOME:-$HOME/.tiup}"

    while [ $# -gt 0 ]; do
        case "$1" in
            -f|--file)     [ $# -ge 2 ] || die "$1 needs a value"; SRC=$2; shift 2 ;;
            --version)     [ $# -ge 2 ] || die "$1 needs a value"; VERSION=$2; shift 2 ;;
            --base-url)    [ $# -ge 2 ] || die "$1 needs a value"; BASE_URL=$2; shift 2 ;;
            --no-verify)   VERIFY=0; shift ;;
            --tiup-home)   [ $# -ge 2 ] || die "$1 needs a value"; TIUP_HOME_DIR=$2; shift 2 ;;
            --no-shell)    NO_SHELL=1; shift ;;
            --uninstall)   UNINSTALL=1; shift ;;
            -h|--help)     usage; exit 0 ;;
            *)             usage >&2; die "unknown option: $1" ;;
        esac
    done

    BIN_DIR="$TIUP_HOME_DIR/bin"
    TARGET="$BIN_DIR/tiup-backup"

    # Pick the startup file for the current user's shell (same idea as the tiup installer).
    case "${SHELL:-}" in
        */bash) RC="$HOME/.bashrc" ;;
        */zsh)  RC="$HOME/.zshrc" ;;
        *)      RC="" ;;
    esac

    remove_rc_block() {
        [ -n "$RC" ] && [ -f "$RC" ] || return 0
        grep -qF "$BEGIN_MARK" "$RC" || return 0
        tmp=$(mktemp)
        sed "/^$BEGIN_MARK\$/,/^$END_MARK\$/d" "$RC" > "$tmp"
        cat "$tmp" > "$RC"   # keep the file's mode / symlink
        rm -f "$tmp"
        return 0
    }

    # ------------------------------------------------------------ uninstall
    if [ "$UNINSTALL" -eq 1 ]; then
        if [ -x "$TARGET" ]; then
            "$TARGET" job --disable >/dev/null 2>&1 || true
            rm -f "$TARGET"
            info "removed $TARGET"
        else
            info "$TARGET not found"
        fi
        if [ -n "$RC" ] && [ -f "$RC" ] && grep -qF "$BEGIN_MARK" "$RC"; then
            remove_rc_block
            info "removed shell function from $RC"
        fi
        info "kept $TIUP_HOME_DIR/storage/meta (backup node list); delete it manually if unwanted"
        exit 0
    fi

    # ------------------------------------------------------------ locate the binary
    [ -d "$BIN_DIR" ] || die "$BIN_DIR does not exist: install tiup first (https://tiup.io) or pass --tiup-home"
    [ -x "$BIN_DIR/tiup" ] || warn "$BIN_DIR/tiup not found; installing anyway"

    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    case "$(uname -m)" in
        x86_64|amd64)  arch=amd64 ;;
        aarch64|arm64) arch=arm64 ;;
        *) die "unsupported architecture: $(uname -m)" ;;
    esac

    WORK=""
    cleanup() { [ -z "$WORK" ] || rm -rf "$WORK"; }
    trap cleanup EXIT INT TERM

    if [ -z "$SRC" ]; then
        # Next to this script, but only when it is a real file named install*.sh: when piped
        # from curl, $0 is just "sh" and must not be mistaken for a script location.
        script_dir=""
        case "$(basename "$0")" in
            install*.sh) [ -f "$0" ] && script_dir=$(cd "$(dirname "$0")" && pwd) ;;
        esac
        if [ -n "$script_dir" ]; then
            for c in "$script_dir/tiup-backup" "$script_dir/tiup-backup-$os-$arch"; do
                if [ -f "$c" ]; then SRC=$c; break; fi
            done
        fi
    fi

    if [ -z "$SRC" ]; then
        [ -n "$BASE_URL" ] || die "no tiup-backup binary next to install.sh and no download URL configured; \
use -f PATH, --base-url URL or TIUP_BACKUP_BASE_URL"
        [ "$os" = "linux" ] || die "no prebuilt package for $os/$arch; build from source or use -f PATH"
        BASE_URL=${BASE_URL%/}
        if [ -n "$VERSION" ]; then
            case "$VERSION" in
                *[!A-Za-z0-9._-]*|"") die "invalid version: $VERSION" ;;
            esac
            case "$BASE_URL" in
                */releases/latest/download) BASE_URL="${BASE_URL%/latest/download}/download/$VERSION" ;;
                *) die "--version only works with a GitHub releases download URL (.../releases/latest/download); use --base-url for $BASE_URL" ;;
            esac
        fi
        pkg="tiup-backup-$os-$arch.tar.gz"
        WORK=$(mktemp -d)
        info "downloading $BASE_URL/$pkg"
        download "$BASE_URL/$pkg" "$WORK/$pkg" || die "download failed: $BASE_URL/$pkg"
        if [ "$VERIFY" -eq 1 ]; then
            download "$BASE_URL/$pkg.sha256" "$WORK/$pkg.sha256" \
                || die "cannot fetch $BASE_URL/$pkg.sha256 (pass --no-verify to skip the check)"
            check_sha256 "$WORK/$pkg" "$WORK/$pkg.sha256"
            info "sha256 ok"
        else
            warn "skipping checksum verification (--no-verify)"
        fi
        SRC="$WORK/$pkg"
    fi
    [ -f "$SRC" ] || die "file not found: $SRC"

    case "$SRC" in
        *.tar.gz|*.tgz)
            [ -n "$WORK" ] || WORK=$(mktemp -d)
            mkdir -p "$WORK/x"
            tar -xzf "$SRC" -C "$WORK/x" || die "cannot extract $SRC"
            found=$(find "$WORK/x" -type f \( -name 'tiup-backup' -o -name "tiup-backup-$os-$arch" \) | head -n 1)
            [ -n "$found" ] || die "no tiup-backup binary inside $SRC"
            SRC=$found
            ;;
    esac

    # ------------------------------------------------------------ install the binary
    NEW="$BIN_DIR/.tiup-backup.$$"
    cp "$SRC" "$NEW" || die "cannot write to $BIN_DIR"
    chmod 755 "$NEW"
    if ! "$NEW" --help >/dev/null 2>&1; then
        rm -f "$NEW"
        die "the binary does not run on this machine ($os/$arch); wrong build?"
    fi
    mv -f "$NEW" "$TARGET"
    info "installed $TARGET"

    # ------------------------------------------------------------ shell integration
    if [ "$NO_SHELL" -eq 1 ]; then
        info "skipped shell integration (--no-shell); run: tiup-backup init-shell"
    elif [ -z "$RC" ]; then
        info "shell '${SHELL:-unknown}' not recognised; add this to your startup file yourself:"
        "$TARGET" init-shell | sed 1d
    elif grep -qF "$BEGIN_MARK" "$RC" 2>/dev/null; then
        info "shell function already present in $RC"
    else
        # make sure the last existing line ends with a newline, but add no blank line,
        # so that --uninstall restores the file exactly
        if [ -s "$RC" ] && [ -n "$(tail -c 1 "$RC")" ]; then printf '\n' >> "$RC"; fi
        {
            printf '%s\n' "$BEGIN_MARK"
            "$TARGET" init-shell | sed 1d
            printf '%s\n' "$END_MARK"
        } >> "$RC"
        info "added shell function to $RC"
    fi

    case ":$PATH:" in
        *":$BIN_DIR:"*) ;;
        *) warn "$BIN_DIR is not on PATH; add: export PATH=$BIN_DIR:\$PATH" ;;
    esac
    if [ "$TIUP_HOME_DIR" != "$HOME/.tiup" ]; then
        warn "custom TiUP home: make sure TIUP_HOME=$TIUP_HOME_DIR is exported in your shell"
    fi

    cat <<EOF

Done. The tiup() function is only picked up by NEW shells. To use it in the
current shell, run this now (an installer cannot do it for you, it runs in
its own process):

  . ${RC:-<your startup file>}

Then try:
  tiup backup list
  tiup backup add -N <ip,...> -u <user>
  tiup backup sync
Note: the function only exists in interactive shells; in scripts/cron call tiup-backup directly.
EOF
}

main "$@"
