#!/usr/bin/env bash
#
# playground-tunnels.sh — Open resilient browser tunnels for Sharko + ArgoCD + Gitea
#
# Opens three kubectl port-forward tunnels (Sharko on 8080, ArgoCD on 18443, Gitea on 13000),
# verifies each one actually accepts connections before declaring success, auto-reconnects a
# tunnel if its pod rolls or restarts mid-session, and tears everything down cleanly on Ctrl+C.
#
# Guards: exits non-zero if sharko-play-hub context doesn't exist, Sharko isn't installed, or
# any of the 3 local ports is held by something that isn't one of our own stale tunnels.

set -euo pipefail

CONTEXT="kind-sharko-play-hub"
SHARKO_NS="sharko"
ARGOCD_NS="argocd"

SHARKO_PORT=8080
ARGOCD_PORT=18443
GITEA_PORT=13000

# --- Helpers ---

err() {
  echo "ERROR: $*" >&2
  exit 1
}

info() {
  echo "==> $*"
}

# --- Guards ---

# Check if context exists
if ! kubectl config get-contexts "$CONTEXT" &>/dev/null; then
  err "Context '$CONTEXT' not found. Run 'make playground-up' first."
fi

# Check if Sharko release is installed
if ! kubectl --context="$CONTEXT" -n "$SHARKO_NS" get deploy sharko &>/dev/null; then
  err "Sharko deployment not found in namespace '$SHARKO_NS'. Run 'make playground-up' first."
fi

# --- State ---

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/sharko-playground-tunnels.XXXXXX")"
STOP_FLAG="$WORKDIR/stop"
TUNNEL_NAMES=(sharko argocd gitea)
LOOP_PIDS=()

# --- Cleanup (Ctrl+C closes all, no orphans left behind) ---

cleanup() {
  local name pidfile kpid pid

  echo ""
  info "Closing tunnels..."

  # Set the stop flag FIRST so no respawn loop restarts a tunnel we're about to kill.
  touch "$STOP_FLAG" 2>/dev/null || true

  # Kill each tunnel's current kubectl child. This also unblocks the loop's `wait`, which
  # then sees the stop flag and exits instead of respawning.
  for name in "${TUNNEL_NAMES[@]}"; do
    pidfile="$WORKDIR/${name}.pid"
    if [ -f "$pidfile" ]; then
      kpid="$(cat "$pidfile" 2>/dev/null || true)"
      if [ -n "$kpid" ]; then
        kill "$kpid" 2>/dev/null || true
      fi
    fi
  done

  # Now stop the respawn loops themselves (defensive — they should already be exiting).
  if [ ${#LOOP_PIDS[@]} -gt 0 ]; then
    for pid in "${LOOP_PIDS[@]}"; do
      kill "$pid" 2>/dev/null || true
    done
  fi

  wait 2>/dev/null || true
  rm -rf "$WORKDIR" 2>/dev/null || true
  echo "All tunnels closed."
}

trap cleanup EXIT INT TERM

# --- Preflight: clear stale listeners left over from a previous run ---

# Prints one PID per line for whatever is LISTENing on the given TCP port on localhost.
listener_pids() {
  local port="$1"
  lsof -nP -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | awk 'NR>1 {print $2}' | sort -u
}

preflight_clear_port() {
  local port="$1"
  local found_stale=0
  local pid cmdline waited

  for pid in $(listener_pids "$port"); do
    cmdline="$(ps -www -p "$pid" -o command= 2>/dev/null || true)"
    if echo "$cmdline" | grep -q 'kubectl' && echo "$cmdline" | grep -q 'port-forward'; then
      info "Clearing stale tunnel on port $port (pid $pid: $cmdline)"
      kill "$pid" 2>/dev/null || true
      found_stale=1
    else
      err "Port $port is already in use by something that isn't one of our tunnels (pid $pid: ${cmdline:-unknown command}). Free it and re-run — e.g.: kill $pid   (details: lsof -nP -iTCP:$port -sTCP:LISTEN)"
    fi
  done

  if [ "$found_stale" -eq 1 ]; then
    waited=0
    while [ -n "$(listener_pids "$port")" ]; do
      if [ "$waited" -ge 20 ]; then
        # Still here after 10s — escalate to SIGKILL once, then give it one last beat.
        for pid in $(listener_pids "$port"); do
          kill -9 "$pid" 2>/dev/null || true
        done
        sleep 1
        break
      fi
      sleep 0.5
      waited=$((waited + 1))
    done
    if [ -n "$(listener_pids "$port")" ]; then
      err "Port $port is still held after clearing stale tunnels and waiting. Check manually: lsof -nP -iTCP:$port -sTCP:LISTEN"
    fi
  fi
}

info "Checking for stale tunnels..."
preflight_clear_port "$SHARKO_PORT"
preflight_clear_port "$ARGOCD_PORT"
preflight_clear_port "$GITEA_PORT"

# --- Open Tunnels (auto-respawn on pod roll / restart) ---

# tunnel_loop keeps a single port-forward alive: (re)start it, wait for it to exit, and unless
# the stop flag is set, sleep briefly and start it again. Each attempt's stderr goes to its own
# log file (never dumped raw to the terminal) so a failure can be diagnosed without noise.
tunnel_loop() {
  local name="$1" pidfile="$2" logfile="$3"
  shift 3
  local first_run=1
  local kpid

  while [ ! -e "$STOP_FLAG" ]; do
    if [ "$first_run" -eq 0 ]; then
      info "$name tunnel reconnecting..."
    fi
    first_run=0

    "$@" >/dev/null 2>"$logfile" &
    kpid=$!
    echo "$kpid" >"$pidfile"

    wait "$kpid" 2>/dev/null || true

    [ -e "$STOP_FLAG" ] && break
    sleep 1.5
  done
}

info "Opening tunnels..."

# 1. Sharko tunnel
tunnel_loop "sharko" "$WORKDIR/sharko.pid" "$WORKDIR/sharko.log" \
  kubectl --context "$CONTEXT" -n "$SHARKO_NS" port-forward svc/sharko "${SHARKO_PORT}:80" &
LOOP_PIDS+=($!)

# 2. ArgoCD tunnel
tunnel_loop "argocd" "$WORKDIR/argocd.pid" "$WORKDIR/argocd.log" \
  kubectl --context "$CONTEXT" -n "$ARGOCD_NS" port-forward svc/argocd-server "${ARGOCD_PORT}:443" &
LOOP_PIDS+=($!)

# 3. Gitea tunnel
tunnel_loop "gitea" "$WORKDIR/gitea.pid" "$WORKDIR/gitea.log" \
  kubectl --context "$CONTEXT" -n "$SHARKO_NS" port-forward svc/gitea "${GITEA_PORT}:3000" &
LOOP_PIDS+=($!)

# --- Verify before claiming up ---

# Returns success if a TCP connection can be opened to 127.0.0.1:port right now.
port_is_open() {
  local port="$1"
  (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null
}

# Polls up to ~10s (20 * 0.5s) for the port to accept a connection.
wait_for_port_up() {
  local port="$1"
  local attempts=0
  while [ "$attempts" -lt 20 ]; do
    if port_is_open "$port"; then
      return 0
    fi
    sleep 0.5
    attempts=$((attempts + 1))
  done
  return 1
}

info "Waiting for tunnels to come up..."

FAILED=""
if ! wait_for_port_up "$SHARKO_PORT"; then
  FAILED="sharko (port $SHARKO_PORT)"
fi
if [ -z "$FAILED" ] && ! wait_for_port_up "$ARGOCD_PORT"; then
  FAILED="argocd (port $ARGOCD_PORT)"
fi
if [ -z "$FAILED" ] && ! wait_for_port_up "$GITEA_PORT"; then
  FAILED="gitea (port $GITEA_PORT)"
fi

if [ -n "$FAILED" ]; then
  echo "" >&2
  echo "ERROR: the $FAILED tunnel did not come up within 10s." >&2
  logname="${FAILED%% *}"
  echo "---- kubectl stderr ($logname) ----" >&2
  cat "$WORKDIR/${logname}.log" 2>/dev/null >&2 || true
  echo "------------------------------------" >&2
  exit 1
fi

# --- Fetch ArgoCD Password ---

ARGOCD_PASSWORD=""
ARGOCD_PASSWORD=$(kubectl --context "$CONTEXT" -n "$ARGOCD_NS" get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || true)

if [ -z "$ARGOCD_PASSWORD" ]; then
  ARGOCD_PASSWORD="<not found — secret argocd-initial-admin-secret missing>"
fi

# --- Display URLs + Logins ---

echo ""
info "Tunnels are up!"
echo ""
echo "  Sharko   ->  http://localhost:8080      login: admin / admin"
echo "  ArgoCD   ->  https://localhost:18443    login: admin / $ARGOCD_PASSWORD   (accept the self-signed cert)"
echo "  Gitea    ->  http://localhost:13000     login: sharko / sharko-play"
echo ""
echo "Tunnels are up. Press Ctrl+C to close them all."
echo "If a pod rolls mid-session, its tunnel reconnects automatically."
echo ""

# Block until interrupted
wait
