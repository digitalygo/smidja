#!/usr/bin/env bash
# run-task.sh <harness:smidja|pi> <task:task1|task2|task3> <trial#>
#
# Runs one benchmark trial: copies a fresh fixture into
# /tmp/smidja-bench/<task>-<harness>-<trial>/, invokes the harness
# non-interactively with the task prompt under an external timeout,
# captures the full transcript to out.log, and runs the post-task
# artifact checks.
#
# Harness invocations:
#   smidja: $SMIDJA_BIN -p "<prompt>"        (model: anthropic/claude-sonnet-4.5)
#   pi:     pi -p "<prompt>" --model anthropic/claude-sonnet-4.5 --no-extensions
#           --no-skills --no-themes --no-prompt-templates --no-context-files
#           --session-dir <trial sessions dir>
#
# Env: OPENROUTER_API_KEY is inherited for smidja; SMIDJA_BIN defaults to
# ./bin/smidja and PI_BIN to pi; BENCH_TIMEOUT_SECS defaults to 300 and
# BENCH_CHECK_TIMEOUT_SECS (post-check phase) to 120.
#
# Execution model: the script runs in two passes. The first pass runs
# the harness (it needs OPENROUTER_API_KEY), then scrubs its exported
# variables and re-executes itself through `/usr/bin/env -i` with an
# explicit allowlist: a fixed PATH=/usr/bin:/bin:/usr/sbin:/sbin, HOME,
# TMPDIR, BENCH_REEXEC=1, and the minimal non-secret state the checks
# and record sections need. Nothing else survives into the second pass's
# exec-time environment - no *_API_KEY, no GITHUB_TOKEN, no AWS_*, no
# SMIDJA_* or PI_* - so a malicious artifact cannot read them from
# /proc/<pid>/environ or inherit them.
#
# Post-check design (verdict = exit code only): the artifact checks run
# as ONE PLAIN SYNCHRONOUS STATEMENT - /usr/bin/timeout -> /usr/bin/
# env -i -> the checks bash - with the checker's stdout and stderr
# redirected to the LITERAL /dev/null (no temp file, no captured stream,
# no path a model-influenced process could swap or hold open). The
# checks bash's EXIT STATUS is the sole verdict signal agreed with the
# parent: 0 = PASS, 10 = FAIL (generic), 11 = FAIL (missing tool); the
# parent maps /usr/bin/timeout's own 124/137 to FAIL (timed out). No
# stream carries the verdict and no pipe exists anywhere in this
# topology: the parent waits only on the checker bash process itself, so
# no descendant - however it escapes - can stall it. The checks bash is
# a pure gate: it performs every check internally (computing all values
# itself) and exits with the agreed codes only; per-check value details
# are no longer recorded automatically (the full transcript remains in
# out.log). Every execution of model-influenced code inside the checks
# script (go test on the trial dir, tools/count.sh, python/jq parsing of
# the model-influenced transcript/answer) redirects BOTH stdout and
# stderr away from the checks process's own stream at the point of
# invocation (`run_sane ... >/dev/null 2>&1`, keeping only the exit
# code; the count.sh value is captured into a checks-owned temp file as
# inert data). Residual: a descendant that itself calls setsid escapes
# timeout's process group and may persist after a timeout group kill;
# accepted - it cannot affect the verdict (exit-code-only), and there is
# no stream or file left for it to write into.
#
# Post-check hygiene: no verdict path or detail file exists anywhere for
# model code to find, unlink or replace: the verdict derives solely from
# the checker's exit status, both checker streams go to the literal
# /dev/null, TASK, LOGDIR, FIXTURE and DIR arrive as positional
# arguments (not environment), and nothing is ever sourced or eval'd.
# The checks run under a FIXED PATH=/usr/bin:/bin and a sanitized
# allowlisted environment (PATH, HOME, TMPDIR, GOFLAGS,
# GOTOOLCHAIN=local, GOPROXY=off) so *_API_KEY, SMIDJA_*, PI_* and other
# secrets never reach a malicious artifact, and Go checks make no network
# calls. go, jq and python3 are resolved exactly once at checks start
# against the fixed PATH; a missing tool is a hard FAIL, never a
# fallback to the caller's PATH (which could hold a model-created
# executable shadowing a system binary).
#
# Trust model: everything the checks or a malicious artifact writes is
# treated as data. The parent never sources or evals any of it, never
# waits on a marker file an artifact could forge, never trusts a pidfile
# an artifact could overwrite, and never blocks on any fd an artifact
# could hold open: with no pipe in the topology, PASS derives solely
# from the trusted checker's exit status. Timeout enforcement is
# /usr/bin/timeout's own: it runs the checks in a fresh process group
# and TERM/KILLs the whole group on expiry (TERM at the
# BENCH_CHECK_TIMEOUT_SECS knob, KILL after 15s). Residual: a descendant
# that itself calls setsid escapes timeout's group and, being same-UID,
# may persist after a group kill; with no pipe, no temp file and only
# literal /dev/null redirects, a stray same-UID descendant has no stream
# or path to stall or taint. Full isolation requires a separate UID or
# container and is explicitly out of scope for this spike.
#
# Per trial it writes out.log (full transcript), answer.json (task1:
# extracted answer, diagnostic artifact), result.txt (summary) into
# /tmp/smidja-bench/logs/<run_id>/, and appends one line to
# /tmp/smidja-bench/results.tsv. The trial dir itself holds only the
# fixture plus whatever the harness changed, so post-run file counts
# stay unambiguous.
set -euo pipefail

usage() {
  echo "usage: run-task.sh <harness:smidja|pi> <task:task1|task2|task3> <trial#>" >&2
  exit 1
}

# ----------------------------------------------------------------------
# First pass: setup + harness invocation. Ends by scrubbing every secret
# from the environment and re-executing this script (BENCH_REEXEC=1),
# which runs only the post-check phase and the record section below.
# ----------------------------------------------------------------------
if [ "${BENCH_REEXEC:-}" != 1 ]; then

  SELF="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
  HARNESS="${1:-}"
  TASK="${2:-}"
  TRIAL="${3:-}"
  case "$HARNESS" in smidja|pi) ;; *) usage ;; esac
  case "$TASK" in task1|task2|task3) ;; *) usage ;; esac
  if ! [[ "$TRIAL" =~ ^[0-9]+$ ]]; then usage; fi

  ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  TASKS_DIR="$ROOT/bench/tasks"
  SMIDJA_BIN="${SMIDJA_BIN:-$ROOT/bin/smidja}"
  PI_BIN="${PI_BIN:-pi}"
  MODEL="anthropic/claude-sonnet-4.5"
  TIMEOUT_SECS="${BENCH_TIMEOUT_SECS:-300}"
  CHECK_TIMEOUT_SECS="${BENCH_CHECK_TIMEOUT_SECS:-120}"

  case "$TASK" in
    task1) TASK_DIR="$TASKS_DIR/task1-inventory" ;;
    task2) TASK_DIR="$TASKS_DIR/task2-bugfix" ;;
    task3) TASK_DIR="$TASKS_DIR/task3-tooling" ;;
  esac
  PROMPT="$(cat "$TASK_DIR/prompt.txt")"
  FIXTURE="$TASK_DIR/fixture"

  BASE="/tmp/smidja-bench"
  RUN_ID="${TASK}-${HARNESS}-${TRIAL}"
  DIR="$BASE/$RUN_ID"
  mkdir -p "$BASE/.sessions" "$BASE/.pi-sessions"
  rm -rf "$DIR"
  mkdir -p "$DIR"
  cp -a "$FIXTURE/." "$DIR/"
  cd "$DIR"

  # ---- harness invocation ----
  case "$HARNESS" in
    smidja)
      if [ ! -x "$SMIDJA_BIN" ]; then
        echo "error: SMIDJA_BIN '$SMIDJA_BIN' is not an executable file" >&2
        exit 1
      fi
      export SMIDJA_MODEL="$MODEL"
      export SMIDJA_SESSION_DIR="$BASE/.sessions/$RUN_ID"
      INV=("$SMIDJA_BIN" -p "$PROMPT")
      BIN_DESC="$SMIDJA_BIN"
      ;;
    pi)
      INV=("$PI_BIN" -p "$PROMPT" --model "$MODEL" \
        --no-extensions --no-skills --no-themes --no-prompt-templates --no-context-files \
        --session-dir "$BASE/.pi-sessions/$RUN_ID")
      BIN_DESC="$PI_BIN"
      ;;
  esac

  if [ -z "${OPENROUTER_API_KEY:-}" ]; then
    echo "warning: OPENROUTER_API_KEY is unset; smidja runs will fail with 401" >&2
  fi

  LOGDIR="$BASE/logs/$RUN_ID"
  mkdir -p "$LOGDIR"

  STARTED="$(date -Iseconds)"
  start=$(date +%s%N)
  set +e
  /usr/bin/timeout -k 10 "$TIMEOUT_SECS" "${INV[@]}" > "$LOGDIR/out.log" 2>&1
  rc=$?
  set -e
  end=$(date +%s%N)
  FINISHED="$(date -Iseconds)"
  wall_ms=$(( (end - start) / 1000000 ))
  timed_out=no
  if [ "$rc" -eq 124 ]; then timed_out=yes; fi

  # ---- scrub secrets, re-exec with an explicit allowlist ----
  # The harness needed the key; nothing after this point does. bash's
  # unset cannot rewrite the exec-time environment that /proc/<pid>/environ
  # exposes, and plain re-execution would still leak every other variable
  # the caller exported (GITHUB_TOKEN, AWS_SECRET_ACCESS_KEY, ...), so we
  # re-execute ourselves through `env -i` with an explicit allowlist:
  # env -i guarantees the second pass's exec-time environment contains
  # exactly the named VAR=value arguments and nothing inherited.
  #
  # The allowlist is built BEFORE the scrub below: the scrub removes
  # every SMIDJA_*/PI_* variable (including SMIDJA_BIN/PI_BIN), so the
  # non-secret values must be captured first.
  # Non-secret state only: fixed PATH, HOME, TMPDIR (if set),
  # BENCH_REEXEC=1, and the minimal values the checks/record sections
  # need. Each variable is an individually named VAR=value argument to
  # env -i, so no other variable can survive into the second pass.
  REEXEC_ENV=(
    PATH=/usr/bin:/bin:/usr/sbin:/sbin
    HOME="$HOME"
    BENCH_REEXEC=1
    RUN_ID="$RUN_ID"
    HARNESS="$HARNESS"
    TASK="$TASK"
    TRIAL="$TRIAL"
    STARTED="$STARTED"
    FINISHED="$FINISHED"
    MODEL="$MODEL"
    BIN_DESC="$BIN_DESC"
    TASK_DIR="$TASK_DIR"
    TIMEOUT_SECS="$TIMEOUT_SECS"
    CHECK_TIMEOUT_SECS="$CHECK_TIMEOUT_SECS"
    rc="$rc"
    timed_out="$timed_out"
    wall_ms="$wall_ms"
    BASE="$BASE"
    ROOT="$ROOT"
    SMIDJA_BIN="$SMIDJA_BIN"
    PI_BIN="$PI_BIN"
  )
  if [ -n "${TMPDIR:-}" ]; then
    REEXEC_ENV+=(TMPDIR="$TMPDIR")
  fi

  unset OPENROUTER_API_KEY 2>/dev/null || true
  while IFS= read -r _secret; do
    case "$_secret" in
      *_API_KEY|SMIDJA_*|PI_*) unset "$_secret" 2>/dev/null || true ;;
    esac
  done < <(compgen -v)
  unset _secret

  exec /usr/bin/env -i "${REEXEC_ENV[@]}" /usr/bin/bash "$SELF" "$@"
  # exec replaces this process; if it failed we already exited.

fi

# ----------------------------------------------------------------------
# Second pass (BENCH_REEXEC=1): post-task artifact checks + record.
# State arrived via the env -i allowlist of the re-exec; the exec-time
# environment of this process contains no secrets.
# ----------------------------------------------------------------------
CHECK_TIMEOUT_SECS="${CHECK_TIMEOUT_SECS:-120}"
DIR="$BASE/$RUN_ID"
LOGDIR="$BASE/logs/$RUN_ID"
FIXTURE="$TASK_DIR/fixture"

# Defensive scrub in case this file ever runs in a single pass.
unset OPENROUTER_API_KEY 2>/dev/null || true
while IFS= read -r _secret; do
  case "$_secret" in
    *_API_KEY|SMIDJA_*|PI_*) unset "$_secret" 2>/dev/null || true ;;
  esac
done < <(compgen -v)
unset _secret

# ---- post-task artifact checks ----
# The entire phase is one plain synchronous statement (no command
# substitution, no pipe, no temp file anywhere): the checks bash runs
# under /usr/bin/timeout -> /usr/bin/env -i with its stdout and stderr
# redirected to the LITERAL /dev/null (nothing to capture, nothing a
# model-influenced process could swap or hold open). The checker's EXIT
# STATUS is the sole verdict signal (0=PASS, 10=FAIL generic, 11=FAIL
# missing tool; timeout's own 124/137 are mapped to FAIL below). No
# detail stream exists: per-check value details are no longer recorded
# automatically (the full transcript remains in out.log) and result.txt
# carries only the generic reason string derived from the exit status.
# The checks receive TASK, LOGDIR, FIXTURE and DIR as positional
# arguments, not as environment: the checks-tree environ contains
# neither a logs-dir path nor any secret.
#
# Timeout enforcement is /usr/bin/timeout's own (knob
# BENCH_CHECK_TIMEOUT_SECS, default 120): it places the checks in a fresh
# process group and TERMs the whole group at the knob, KILLing it 15s
# later (--kill-after=15s). rc 124/137 from the statement means the
# checks timed out. Residual: a descendant that itself calls setsid
# escapes timeout's group and, being same-UID, may persist after a
# timeout group kill; on normal completion timeout does not sweep the
# group either, so a same-UID stray descendant can linger. Accepted:
# with no pipe, no temp file and only literal /dev/null redirects, a
# stray descendant cannot stall the parent (the parent waits only on the
# checker bash process), cannot forge the verdict (exit-code-only), and
# has no stream or file to write into. Because every model-influenced
# execution also runs with stdout and stderr redirected to /dev/null, or
# - for the count.sh value - to a checks-owned temp file, nothing
# model-influenced writes to any stream at all in the normal course.
#
# Everything that executes or parses model-influenced content (go test,
# tools/count.sh, python/jq helpers) runs with a sanitized allowlisted
# environment built via `env -i` with a FIXED PATH=/usr/bin:/bin (PATH,
# HOME, TMPDIR, GOFLAGS, GOTOOLCHAIN=local, GOPROXY=off): no *_API_KEY,
# SMIDJA_*, PI_*, GITHUB_TOKEN, AWS_* or any other secret can reach
# model-created code, the caller's PATH can never shadow a system binary,
# and Go checks make no network calls. go, jq and python3 are resolved
# exactly once at checks start against the fixed PATH; a missing tool is
# a hard FAIL, never a fallback.
CHECK="FAIL"
check_detail=""

# Minimal allowlist for the checks process tree. No CHECK_* variable and
# no path is exported: the checks get TASK/LOGDIR/FIXTURE/DIR as argv.
CHECK_LAUNCH_ENV=(
  PATH=/usr/bin:/bin
  HOME="$HOME"
  GOTOOLCHAIN=local
  GOPROXY=off
)
if [ -n "${TMPDIR:-}" ]; then
  CHECK_LAUNCH_ENV+=(TMPDIR="$TMPDIR")
fi
if [ -n "${GOFLAGS:-}" ]; then
  CHECK_LAUNCH_ENV+=(GOFLAGS="$GOFLAGS")
fi

# One plain synchronous statement: timeout owns the deadline, env -i owns
# the allowlist, and the checks script travels on the checks bash's stdin
# as a quoted heredoc (authored by this parent, never model-influenced; a
# quoted heredoc avoids every bash -c quoting hazard and materializes no
# script file anywhere). The checker's stdout and stderr are redirected
# to the LITERAL /dev/null: there is NO temp file, NO captured stream and
# NO pipe anywhere in this topology, so no descendant - however it
# escapes - can stall the parent (nothing exists for it to hold open) and
# no path exists that a model-influenced process could swap. The verdict
# is $check_rc, captured immediately after the statement returns.
#
# Residual: a setsid-escaped same-UID descendant may persist after a
# timeout group kill (timeout cannot reach a new session). Accepted: it
# cannot affect the verdict (exit-code-only) and there is no stream or
# file left for it to write into.
set +e
/usr/bin/timeout --signal=TERM --kill-after=15s "$CHECK_TIMEOUT_SECS" \
  /usr/bin/env -i "${CHECK_LAUNCH_ENV[@]}" \
  /usr/bin/bash -s "$TASK" "$LOGDIR" "$FIXTURE" "$DIR" > /dev/null 2> /dev/null <<'CHECK_EOF'
set -euo pipefail

# Positional arguments from the parent: TASK, LOGDIR, FIXTURE, DIR.
# They travel as argv, never as environment, so the checks-tree environ
# holds only the fixed allowlist above and no logs-dir path.
CHECK_TASK="$1"
CHECK_LOGDIR="$2"
CHECK_FIXTURE="$3"
CHECK_DIR="$4"

# The checks bash is a pure gate. It performs every check internally,
# comparing values it computed itself, and its EXIT STATUS is the sole
# verdict signal agreed with the parent: 0 = PASS, 10 = FAIL (generic),
# 11 = FAIL (missing tool); the timeout's own 124/137 are mapped by the
# parent. This process's stdout and stderr go to the literal /dev/null:
# no diagnostic text is echoed (per-check value details are no longer
# recorded) and nothing decision-relevant is ever written to a stream.
# Every command that executes model-influenced code (go test on the
# trial dir, tools/count.sh) or parses model-influenced content (the
# transcript, the extracted answer) runs with stdout and stderr
# redirected away from this process's own stream - `/dev/null`, keeping
# only the exit code. There is no pipe anywhere in this topology, so no
# descendant can stall the parent, and with /dev/null streams there is
# nothing a rogue descendant could reopen and write into.

# Minimal allowlist for anything that executes or parses model-influenced
# content: no *_API_KEY, no SMIDJA_*, no PI_*, no network for Go. The
# child already launched with this allowlist (fixed PATH=/usr/bin:/bin);
# rebuilding it keeps run_sane meaningful even if the launch env changes
# later.
sane_env=()
for name in PATH HOME TMPDIR; do
  if [ -n "${!name:-}" ]; then
    sane_env+=("$name=${!name}")
  fi
done
if [ -n "${GOFLAGS:-}" ]; then
  sane_env+=("GOFLAGS=$GOFLAGS")
fi
sane_env+=(GOTOOLCHAIN=local GOPROXY=off)

# Run a command with the sanitized allowlisted environment.
run_sane() {
  /usr/bin/env -i "${sane_env[@]}" "$@"
}

# Resolve the tools the checks need exactly once, against the fixed
# system PATH only. A `command -v` run against the caller's PATH could
# prefer a model-created executable in a user-writable directory earlier
# in PATH; the fixed PATH can only resolve system binaries. A missing
# tool is a hard failure (exit 11), never a fallback to another PATH.
GO=""; JQ=""; PY3=""; _missing=""
for _tool in go jq python3; do
  if _path="$(PATH=/usr/bin:/bin command -v "$_tool" 2>/dev/null)"; then
    case "$_tool" in
      go) GO="$_path" ;;
      jq) JQ="$_path" ;;
      python3) PY3="$_path" ;;
    esac
  else
    _missing="$_missing $_tool"
  fi
done

if [ -n "$_missing" ]; then
  exit 11
fi

case "$CHECK_TASK" in
  task1)
    # Expected values are computed here, by this script, from the frozen
    # source fixture (its own awk/find/jq/wc against paths it knows).
    exp_module="$(awk '/^module[ \t]/{print $2; exit}' "$CHECK_FIXTURE/go.mod")"
    exp_files="$(cd "$CHECK_FIXTURE" && printf '%s\n' *.go | sort | run_sane "$JQ" -cR -s 'split("\n") | map(select(length > 0))')"
    exp_lines="$(cd "$CHECK_FIXTURE" && cat *.go | wc -l)"
    task1_ok=no
    # Extract the JSON answer from the (model-influenced) transcript and
    # compare it against the expected values. Both python and jq run with
    # stdout and stderr discarded - only their exit codes are used. The
    # extracted object is also written to answer.json as a diagnostic
    # artifact (a file, never a stream to the parent; the verdict still
    # comes solely from the exit status).
    if run_sane "$PY3" - "$CHECK_LOGDIR/out.log" "$CHECK_LOGDIR/answer.json" >/dev/null 2>&1 <<'PYEOF'
import json, re, sys

def extract(text):
    for m in re.finditer(r"```(?:json)?\s*(.*?)```", text, re.S):
        try:
            obj = json.loads(m.group(1))
            if isinstance(obj, dict):
                return obj
        except Exception:
            pass
    i = 0
    while True:
        i = text.find("{", i)
        if i < 0:
            return None
        depth, in_str, esc = 0, False, False
        for j in range(i, len(text)):
            ch = text[j]
            if in_str:
                if esc:
                    esc = False
                elif ch == "\\":
                    esc = True
                elif ch == '"':
                    in_str = False
                continue
            if ch == '"':
                in_str = True
            elif ch == "{":
                depth += 1
            elif ch == "}":
                depth -= 1
                if depth == 0:
                    try:
                        obj = json.loads(text[i:j + 1])
                        if isinstance(obj, dict):
                            return obj
                    except Exception:
                        break
        i += 1
    return None

obj = extract(open(sys.argv[1], encoding="utf-8", errors="replace").read())
if obj is None:
    sys.exit(1)
with open(sys.argv[2], "w", encoding="utf-8") as f:
    json.dump(obj, f, sort_keys=True)
PYEOF
    then
      if run_sane "$JQ" -e --arg m "$exp_module" --argjson f "$exp_files" --argjson l "$exp_lines" \
          '.module == $m and .files == $f and .lines == $l' "$CHECK_LOGDIR/answer.json" >/dev/null 2>&1
      then
        task1_ok=yes
      fi
    fi
    [ "$task1_ok" = yes ] && exit 0 || exit 10
    ;;
  task2)
    # Tests must pass, the test file must be untouched, and the exported
    # API (func MungeCWD) must still exist.
    tests_unchanged=no
    if cmp -s "$CHECK_FIXTURE/munge_test.go" "$CHECK_DIR/munge_test.go"; then
      tests_unchanged=yes
    fi
    api_present=no
    if grep -q '^func MungeCWD' "$CHECK_DIR/munge.go" 2>/dev/null; then
      api_present=yes
    fi
    # go test executes the model-modified package: both streams are
    # discarded, only the exit code is kept.
    set +e
    (cd "$CHECK_DIR" && run_sane "$GO" test ./... >/dev/null 2>&1)
    gotest_rc=$?
    set -e
    task2_ok=no
    if [ "$tests_unchanged" = yes ] && [ "$api_present" = yes ] && [ "$gotest_rc" -eq 0 ]; then
      task2_ok=yes
    fi
    [ "$task2_ok" = yes ] && exit 0 || exit 10
    ;;
  task3)
    # tools/count.sh must exist and be executable, and its output must
    # equal the real count of regular files in the trial dir (the 12
    # seeded files plus count.sh itself).
    script_ok=no
    if [ -x "$CHECK_DIR/tools/count.sh" ]; then
      script_ok=yes
    fi
    expected_files="$(find "$CHECK_FIXTURE" -type f | wc -l)"
    ground="$(find "$CHECK_DIR" -type f | wc -l)"
    expected=$((expected_files + 1))
    reported=""
    if [ "$script_ok" = yes ]; then
      # tools/count.sh is model-created code: it must never inherit this
      # process's stdout (which goes to the literal /dev/null). Its
      # output is captured into a checks-owned temp file as inert data,
      # with stderr to /dev/null; only the exit code matters, and the
      # verdict still comes solely from this script's exit status.
      tmp="$(mktemp)"
      set +e
      (cd "$CHECK_DIR" && run_sane ./tools/count.sh >"$tmp" 2>/dev/null)
      set -e
      reported="$(grep -oE '[0-9]+' "$tmp" | tail -1 || true)"
      rm -f "$tmp"
    fi
    task3_ok=no
    if [ "$script_ok" = yes ] && [ "$ground" -eq "$expected" ] && [ "$reported" = "$ground" ]; then
      task3_ok=yes
    fi
    [ "$task3_ok" = yes ] && exit 0 || exit 10
    ;;
  *)
    exit 10
    ;;
esac
CHECK_EOF
check_rc=$?
set -e

# The verdict derives solely from the checks bash's exit status, mapped
# below by plain assignments only (no command substitution anywhere in
# the post-check section): rc 124/137 means timeout fired - the checks
# timed out regardless of any partial output. Per-check value details
# are no longer recorded; result.txt and the TSV carry the generic
# reason string only, and the full transcript remains in out.log.
if [ "$check_rc" -eq 0 ]; then
  CHECK="PASS"
  check_detail="passed"
elif [ "$check_rc" -eq 124 ] || [ "$check_rc" -eq 137 ]; then
  CHECK="FAIL"
  check_detail="post-check phase timed out after ${CHECK_TIMEOUT_SECS}s"
else
  CHECK="FAIL"
  check_detail="checks failed (rc=$check_rc)"
fi

# ---- record ----
{
  echo "run_id: $RUN_ID"
  echo "harness: $HARNESS"
  echo "task: $TASK"
  echo "trial: $TRIAL"
  echo "started: $STARTED"
  echo "finished: $FINISHED"
  echo "model: $MODEL"
  echo "bin: $BIN_DESC"
  echo "prompt_file: bench/tasks/${TASK_DIR##*/}/prompt.txt"
  echo "timeout_secs: $TIMEOUT_SECS"
  echo "check_timeout_secs: $CHECK_TIMEOUT_SECS"
  echo "exit_code: $rc"
  echo "timed_out: $timed_out"
  echo "wall_ms: $wall_ms"
  echo "out_log: $LOGDIR/out.log"
  echo "check: $CHECK"
  echo "check_detail: $check_detail"
} > "$LOGDIR/result.txt"

TSV="$BASE/results.tsv"
if [ ! -f "$TSV" ]; then
  echo -e "run_id\tharness\ttask\ttrial\twall_ms\texit_code\ttimed_out\tcheck\tdetail" > "$TSV"
fi
# One line per trial: the detail is a single-line generic reason string
# (parent-owned, no command substitution), so no flattening is needed.
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
  "$RUN_ID" "$HARNESS" "$TASK" "$TRIAL" "$wall_ms" "$rc" "$timed_out" "$CHECK" "$check_detail" >> "$TSV"

echo "== $RUN_ID =="
echo "  wall_ms=$wall_ms exit_code=$rc timed_out=$timed_out"
echo "  check=$CHECK ($check_detail)"
echo "  details: $LOGDIR/result.txt, transcript: $LOGDIR/out.log"
