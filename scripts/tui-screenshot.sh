#!/usr/bin/env bash
# Runs the real ./cleanup binary in a detached tmux pane, sends keys, captures the
# screen and renders it to PNG (scripts/ansi2png.mjs). Development only.
#
#   scripts/tui-screenshot.sh OUT.png "KEYS" [cleanup args...]
#
# KEYS is a space-separated tmux send-keys list (e.g. "Down Down Space"), sent after
# the scan settles. Env: COLS (132), ROWS (34), WAIT (3s). THEME=light only changes
# the page background; the TUI picks its palette from what tmux reports (dark).
set -euo pipefail
out=$1 keys=$2; shift 2
cols=${COLS:-132} rows=${ROWS:-34} theme=${THEME:-dark} wait=${WAIT:-3}
here=$(cd "$(dirname "$0")/.." && pwd)
sock=cleanup-shot-$$
conf=$(mktemp) ansi=$(mktemp)
trap 'tmux -L "$sock" kill-server 2>/dev/null || true; rm -f "$conf" "$ansi"' EXIT
printf 'set -g default-terminal "tmux-256color"\nset -ga terminal-overrides ",*:Tc"\nset -g status off\n' >"$conf"
cmd=$(printf '%q ' "$here/cleanup" "$@")
tmux -f "$conf" -L "$sock" new-session -d -x "$cols" -y "$rows" -s s \
  "cd $(printf %q "$here") && env TERM=xterm-256color COLORTERM=truecolor $cmd; sleep 600"
sleep "$wait"
for k in $keys; do tmux -L "$sock" send-keys -t s "$k"; sleep 0.15; done
sleep 0.5
tmux -L "$sock" capture-pane -t s -e -p >"$ansi"
node "$here/scripts/ansi2png.mjs" "$ansi" "$out" "$cols" "$theme"
echo "$out"
