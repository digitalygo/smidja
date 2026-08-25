#!/usr/bin/env bash
# metrics.sh <command> [arg...]
#
# Measures two properties of a runnable CLI:
#
#   1. Startup latency: median and p95 wall time of the trivial invocation
#      `<command> [arg...] --version` (10 warmups, then 100 runs by
#      default), in milliseconds.
#
#   2. Idle RSS: median and p95 resident set size of the process tree
#      while the CLI sits idle waiting for input, in kilobytes. The
#      command is started with its stdin held open by a pipe that delivers
#      nothing, so a REPL-style harness blocks reading input; after 2
#      seconds the RSS of the whole process tree (command plus children)
#      is summed via ps. Repeated 20 times by default.
#
# The command must be a CLI that prints a version for `--version` and
# blocks on stdin when stdin is a pipe (REPL mode). For smidja pass the
# plain binary; for pi pass the minimal-flags template used by
# bench/run-task.sh (see docs/benchmarks/phase-0.md).
#
# Knobs (env):
#   BENCH_WARMUP_RUNS     startup warmup runs (default 10)
#   BENCH_STARTUP_RUNS    measured startup runs (default 100)
#   BENCH_IDLE_REPS       idle RSS samples (default 20)
#   BENCH_IDLE_WAIT_SECS  seconds to wait before sampling (default 2)
#   BENCH_HOLD_SECS       seconds stdin is held open (default 30)
#
# Output: human-readable lines followed by key=value lines for capture.
set -euo pipefail

CMD="${1:-}"
if [ -z "$CMD" ]; then
  echo "usage: metrics.sh <command> [arg...]" >&2
  exit 1
fi
shift
ARGS=("$@")

WARMUP="${BENCH_WARMUP_RUNS:-10}"
RUNS="${BENCH_STARTUP_RUNS:-100}"
REPS="${BENCH_IDLE_REPS:-20}"
WAIT="${BENCH_IDLE_WAIT_SECS:-2}"
HOLD="${BENCH_HOLD_SECS:-30}"

# percentile_stats: reads one value per line on stdin, prints
# "median p95" (nearest-rank p95, median = middle value, average of the
# two middle values for an even count). DIV is the divisor applied to
# the raw values (1e6 to convert nanoseconds to milliseconds, 1 for
# already-unit values such as KB).
percentile_stats() {
  local div="${1:-1}"
  awk -v d="$div" '
    { a[NR] = $1; n = NR }
    END {
      for (i = 1; i <= n; i++)
        for (j = i + 1; j <= n; j++)
          if (a[j] < a[i]) { t = a[i]; a[i] = a[j]; a[j] = t }
      if (n % 2 == 1) med = a[(n + 1) / 2]
      else med = (a[n / 2] + a[n / 2 + 1]) / 2
      idx = int(0.95 * n)
      if (idx * 100 < 95 * n) idx++
      p95 = a[idx]
      printf "%.2f %.2f\n", med / d, p95 / d
    }'
}

# tree_rss: sum of RSS (KB) of a process and all its descendants.
tree_rss() {
  local p="$1" total=0 rss c
  rss=$(ps -o rss= -p "$p" 2>/dev/null) || { echo 0; return; }
  total=$((rss))
  for c in $(pgrep -P "$p" 2>/dev/null); do
    total=$((total + $(tree_rss "$c")))
  done
  echo "$total"
}

# ---------- 1. startup latency ----------
for _ in $(seq 1 "$WARMUP"); do
  "$CMD" "${ARGS[@]}" --version >/dev/null 2>&1 || true
done

times_file="$(mktemp)"
trap 'rm -f "$times_file"' EXIT
for _ in $(seq 1 "$RUNS"); do
  s=$(date +%s%N)
  "$CMD" "${ARGS[@]}" --version >/dev/null 2>&1 || true
  e=$(date +%s%N)
  echo "$((e - s))" >> "$times_file"
done
read -r startup_median_ms startup_p95_ms < <(percentile_stats 1000000 < "$times_file")
rm -f "$times_file"
trap - EXIT

# ---------- 2. idle RSS ----------
rss_file="$(mktemp)"
trap 'rm -f "$rss_file"' EXIT
live=0
for _ in $(seq 1 "$REPS"); do
  fifo="$(mktemp -u)"
  mkfifo "$fifo"
  # Writer opens the fifo and keeps it open for $HOLD seconds; the
  # harness reads stdin from the fifo, so it blocks idle on input.
  sleep "$HOLD" >"$fifo" 2>/dev/null &
  sleep_pid=$!
  "$CMD" "${ARGS[@]}" <"$fifo" >/dev/null 2>&1 &
  pid=$!
  sleep "$WAIT"
  if kill -0 "$pid" 2>/dev/null; then
    live=$((live + 1))
  fi
  echo "$(tree_rss "$pid")" >> "$rss_file"
  kill "$pid" "$sleep_pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  wait "$sleep_pid" 2>/dev/null || true
  rm -f "$fifo"
done
read -r idle_median_kb idle_p95_kb < <(percentile_stats 1 < "$rss_file")
rm -f "$rss_file"
trap - EXIT

# ---------- report ----------
echo "startup: ${RUNS} runs of '$CMD ${ARGS[*]} --version' (${WARMUP} warmups discarded)"
echo "  median ${startup_median_ms} ms, p95 ${startup_p95_ms} ms"
echo "idle rss: ${REPS} samples, stdin held open ${HOLD}s, sampled after ${WAIT}s, tree RSS summed"
echo "  live samples ${live}/${REPS}, median ${idle_median_kb} KB, p95 ${idle_p95_kb} KB"
echo
echo "startup_runs=${RUNS}"
echo "startup_warmup=${WARMUP}"
echo "startup_median_ms=${startup_median_ms}"
echo "startup_p95_ms=${startup_p95_ms}"
echo "idle_reps=${REPS}"
echo "idle_hold_secs=${HOLD}"
echo "idle_wait_secs=${WAIT}"
echo "idle_live_samples=${live}"
echo "idle_median_kb=${idle_median_kb}"
echo "idle_p95_kb=${idle_p95_kb}"
