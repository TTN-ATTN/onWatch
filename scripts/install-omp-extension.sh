#!/usr/bin/env bash
# install-omp-extension.sh - Add (or remove) the onWatch usage reporter in omp
#
# Installs examples/api_integrations/omp/onwatch.ts as an omp extension, so every
# new omp session reports its model calls to onWatch. Run from a checkout, or
# pipe it from GitHub (the extension is then downloaded).
#
#   scripts/install-omp-extension.sh                 # copy into ~/.omp/agent/extensions
#   scripts/install-omp-extension.sh --link          # symlink; `git pull` updates it
#   scripts/install-omp-extension.sh --profile work  # an omp --profile agent dir
#   scripts/install-omp-extension.sh --uninstall
#
# Environment: PI_CODING_AGENT_DIR (omp agent dir, default profile only),
# ONWATCH_API_INTEGRATIONS_DIR (where events are written), ONWATCH_OMP_EXTENSION_URL.

set -euo pipefail

MARKER="onWatch usage reporter for omp"
URL="${ONWATCH_OMP_EXTENSION_URL:-https://raw.githubusercontent.com/TTN-ATTN/onWatch/main/examples/api_integrations/omp/onwatch.ts}"

mode=copy
profile=""
force=false

usage() {
    cat <<'EOF'
Usage: install-omp-extension.sh [--link] [--profile NAME] [--uninstall] [--force]

Installs the onWatch usage reporter into omp, so every new omp session reports
its model calls (tokens and timing only) to onWatch.

  (no option)       copy the extension into ~/.omp/agent/extensions
  --link            symlink it to this checkout instead; `git pull` updates it
  --profile NAME    use omp's --profile NAME agent directory
  --uninstall       remove the extension
  --force           replace an unrelated extensions/onwatch.ts
EOF
}
info() { printf '[onwatch-omp] %s\n' "$*"; }
fail() { printf '[onwatch-omp] error: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
    case "$1" in
        --link) mode=link ;;
        --uninstall) mode=uninstall ;;
        --profile)
            [ $# -ge 2 ] || fail "--profile needs a name"
            profile="$2"; shift ;;
        --profile=*) profile="${1#--profile=}" ;;
        --force) force=true ;;
        -h|--help) usage; exit 0 ;;
        *) fail "unknown option: $1 (see --help)" ;;
    esac
    shift
done

case "$profile" in
    ""|*[!A-Za-z0-9._-]*) [ -z "$profile" ] || fail "invalid profile name: $profile" ;;
esac

# omp's agent directory: a named profile, PI_CODING_AGENT_DIR, or the default.
if [ -n "$profile" ]; then
    agent_dir="$HOME/.omp/profiles/$profile/agent"
elif [ -n "${PI_CODING_AGENT_DIR:-}" ]; then
    agent_dir="${PI_CODING_AGENT_DIR/#\~/$HOME}"
else
    agent_dir="$HOME/.omp/agent"
fi
target="$agent_dir/extensions/onwatch.ts"

# Never replace a file that is not ours (another extension named onwatch.ts).
ours() { [ ! -e "$1" ] || grep -q "$MARKER" "$1" 2>/dev/null; }
if [ -e "$target" ] || [ -L "$target" ]; then
    if ! ours "$target" && ! $force; then
        fail "$target exists and is not the onWatch reporter; move it away or pass --force"
    fi
fi

if [ "$mode" = uninstall ]; then
    if [ -e "$target" ] || [ -L "$target" ]; then
        rm -f "$target"
        info "removed $target (running omp sessions keep it until they restart)"
    else
        info "not installed in $agent_dir"
    fi
    exit 0
fi

# Prefer the extension next to this script; fall back to downloading it (also
# when the script is piped into bash and has no file of its own).
script_dir=""
if [ -f "${BASH_SOURCE[0]:-}" ]; then
    script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fi
source_file="${script_dir:+$script_dir/../examples/api_integrations/omp/onwatch.ts}"
tmp=""
cleanup() { [ -z "$tmp" ] || rm -f "$tmp"; }
trap cleanup EXIT
if [ -z "$source_file" ] || [ ! -f "$source_file" ]; then
    [ "$mode" = copy ] || fail "--link needs an onWatch checkout; run this script from the repository"
    command -v curl >/dev/null 2>&1 || fail "curl is required to download the extension"
    tmp="$(mktemp)"
    info "downloading $URL"
    curl -fsSL "$URL" -o "$tmp" || fail "download failed; run this script from an onWatch checkout instead"
    source_file="$tmp"
fi
grep -q "$MARKER" "$source_file" || fail "$source_file is not the onWatch reporter"
source_file="$(cd "$(dirname "$source_file")" && pwd)/$(basename "$source_file")"

mkdir -p "$agent_dir/extensions"
if [ "$mode" = link ]; then
    if [ "$(readlink "$target" 2>/dev/null || true)" = "$source_file" ]; then
        info "already linked: $target -> $source_file"
    else
        ln -sfn "$source_file" "$target"
        info "linked $target -> $source_file"
    fi
elif [ -f "$target" ] && [ ! -L "$target" ] && cmp -s "$source_file" "$target"; then
    info "already up to date: $target"
else
    rm -f "$target"
    install -m 0644 "$source_file" "$target"
    info "installed $target"
fi

# Pre-create the events directory with private permissions, as the extension would.
events_dir="${ONWATCH_API_INTEGRATIONS_DIR:-$HOME/.onwatch/api-integrations}"
events_dir="${events_dir/#\~/$HOME}"
mkdir -p "$events_dir" && chmod 700 "$events_dir"
info "omp model calls will be written to $events_dir/omp-YYYY-MM.jsonl"

command -v omp >/dev/null 2>&1 || info "note: omp is not on PATH; the extension loads once omp is installed"
if command -v onwatch >/dev/null 2>&1; then
    onwatch status >/dev/null 2>&1 || info "note: onWatch is not running; start it with: onwatch"
fi
info "done. New omp sessions report usage; restart running sessions to pick it up."
