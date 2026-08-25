# Phase 0 benchmark harness

This document describes the Phase 0 spike benchmark for the Smidja harness
compared against Pi (version 0.84.2). The harness lives in `bench/` and
measures three things: startup latency, idle memory footprint, and
success on three small coding tasks.

The result tables in this document are intentionally left empty. The
orchestrator fills them in after the real runs, together with the
environment values captured on the run machine.

## Scope and ownership

The benchmark owns only two paths:

- `bench/` (bash scripts and task fixtures)
- `docs/benchmarks/phase-0.md` (this document)

The smidja binary is built separately by the orchestrator with
`bench/build.sh` or any equivalent build; every script accepts its path
through the `SMIDJA_BIN` environment variable (default `./bin/smidja`).

## Environment capture

Capture the environment on the run machine with these commands and
record the output in the table below before starting a run series.

```bash
uname -srmo
uname -r
grep -m1 "model name" /proc/cpuinfo
grep -c ^processor /proc/cpuinfo
grep -m1 "MemTotal" /proc/meminfo
go version
pi --version
date -Iseconds
```

| Fact | Command | Value |
| --- | --- | --- |
| Kernel and architecture | `uname -srmo` | Linux 7.1.4-200.fc44.x86_64 x86_64 GNU/Linux |
| Kernel release | `uname -r` | 7.1.4-200.fc44.x86_64 |
| CPU model | `grep -m1 "model name" /proc/cpuinfo` | 11th Gen Intel(R) Core(TM) i7-1165G7 @ 2.80GHz |
| CPU count | `grep -c ^processor /proc/cpuinfo` | 8 |
| Total memory | `grep -m1 "MemTotal" /proc/meminfo` | 16029436 kB |
| Go toolchain | `go version` | go1.26.6 |
| Pi version | `pi --version` | 0.84.2 |
| Run date | `date -Iseconds` | 2026-08-25T03:17:45+02:00 |

Reference snapshot captured while writing this document (2026-08-25):
Linux 7.1.4-200.fc44.x86_64 x86_64 GNU/Linux, 11th Gen Intel Core
i7-1165G7 @ 2.80GHz (8 processors), 16029436 kB total memory, Go
go1.26.6, Pi 0.84.2.

## Metrics methodology

### Startup latency

Measured by `bench/metrics.sh <command> [arg...]`.

The trivial invocation is `--version` on the harness:

- smidja: `./bin/smidja --version` prints `smidja <version>` and exits.
- pi: `pi --version` prints the version and exits.

Procedure, per run:

1. Warmup: run the invocation 10 times, discarding the timings.
2. Measure: run the invocation 100 times, timing each with
   `date +%s%N` deltas around the call (nanoseconds, converted to ms).
3. Statistics: sort the 100 samples, then compute the median (middle
   value; average of the two middle values for an even count) and the
   p95 (nearest-rank, index `ceil(0.95 * n)`) with `awk`.

Both numbers are reported in milliseconds. The default counts can be
changed with `BENCH_WARMUP_RUNS` and `BENCH_STARTUP_RUNS`.

The timing loop measures the shell's fork and exec of the CLI plus the
CLI's own startup, which is the wall time an interactive user sees.

### Idle RSS

Measured by `bench/metrics.sh <command> [arg...]`.

The goal is the resident set size of the harness while it sits ready for
input but does no work. Each sample works like this:

1. Create a FIFO and hold its write end open with a background
   `sleep 30`; start the harness with its stdin reading from the FIFO.
   The harness therefore blocks on stdin exactly like in
   `sleep 30 | <harness>`, but with both PIDs known precisely.
2. Wait 2 seconds for the harness to reach its idle state.
3. Sum the RSS in kilobytes of the whole process tree (the harness PID
   plus all descendants, discovered with `pgrep -P`) via `ps -o rss=`.
4. Kill the harness and the sleep, discard the FIFO.

Repeat 20 times. Report the median and nearest-rank p95 of the 20
samples, in kilobytes. Samples where the process had already exited
before the 2 second wait are recorded as 0 KB; the number of live
samples is reported separately (`idle_live_samples`).

Knobs: `BENCH_IDLE_REPS` (default 20), `BENCH_IDLE_WAIT_SECS` (default
2), `BENCH_HOLD_SECS` (default 30).

The command template matters:

- smidja: `bench/metrics.sh ./bin/smidja`. With stdin a pipe, smidja
  starts its REPL, prints `> `, and blocks reading input.
- pi: `bench/metrics.sh pi --no-extensions --no-skills --no-themes
  --no-prompt-templates --no-context-files`. Same flags as the task
  runner, so no extensions or skills load in either harness.

The tree walk is needed because pi resolves through a shim chain
(`/usr/bin/pi` is a sh wrapper that execs `mise`, which execs
`node dist/cli.js`); the process tree can contain more than one PID.

### Task runs

Measured by `bench/run-task.sh <harness:smidja|pi> <task:task1|task2|task3> <trial#>`.

Each trial:

1. Creates a fresh fixture directory
   `/tmp/smidja-bench/<task>-<harness>-<trial>/` by copying the frozen
   template from `bench/tasks/<task>/fixture/`.
2. Runs the harness non-interactively with the task prompt under an
   external `timeout 300` (knob `BENCH_TIMEOUT_SECS`), capturing the
   full transcript to `/tmp/smidja-bench/logs/<run_id>/out.log`.
3. Records wall time (`date +%s%N` deltas), exit code, and a timeout
   flag (exit 124 means the timeout fired).
4. Runs the per-task artifact checks and writes
   `/tmp/smidja-bench/logs/<run_id>/result.txt`, appending one line to
   `/tmp/smidja-bench/results.tsv`.

Harness invocations:

| Harness | Command |
| --- | --- |
| smidja | `$SMIDJA_BIN -p "<prompt>"` |
| pi | `pi -p "<prompt>" --model anthropic/claude-sonnet-4.5 --no-extensions --no-skills --no-themes --no-prompt-templates --no-context-files --session-dir <dir>` |

Environment hygiene inside the runner:

- The full environment is inherited, so `OPENROUTER_API_KEY` reaches
  smidja unchanged. A warning is printed when it is unset.
- Model alignment: smidja runs with `SMIDJA_MODEL` pinned to
  `anthropic/claude-sonnet-4.5` (its compiled-in default), and pi is
  passed `--model anthropic/claude-sonnet-4.5` explicitly.
- Session isolation: smidja writes sessions under
  `/tmp/smidja-bench/.sessions/<run_id>/` via `SMIDJA_SESSION_DIR`, and
  pi gets `--session-dir /tmp/smidja-bench/.pi-sessions/<run_id>/`.
  Neither harness pollutes the user's real session store.
- `PI_BIN` overrides the pi executable (default `pi`), mirroring
  `SMIDJA_BIN`.

## Task fixtures

### task1-inventory

Fixture: `bench/tasks/task1-inventory/fixture/` with a tiny Go module
(`example.com/inventory`, two files `main.go` and `util.go`, 13 lines
of Go total).

Prompt (English):

```text
Inspect this repository and reply ONLY with a JSON object with keys module, files (sorted list of .go file names), and lines (total line count of .go files). Do not create or modify any file.
```

Expected answer: `module` = `example.com/inventory`, `files` =
`["main.go","util.go"]`, `lines` = 13.

Check: a Python helper extracts the first balanced JSON object from the
transcript (fenced code blocks tolerated), then `jq` compares it
strictly against ground truth computed from the frozen source fixture
(not from the trial dir, so tampering cannot influence the truth).

### task2-bugfix

Fixture: `bench/tasks/task2-bugfix/fixture/` with a seeded bug.
`munge.go` contains `func MungeCWD(cwd string) string` that emits one
dash per separator and an extra trailing dash, so repeated separators
produce wrong output, for example `MungeCWD("a//b")` returns `"a--b-"`
instead of `"a-b"`. `munge_test.go` contains `TestMungeCWD` with six
cases that all fail on the seeded bug. Verified before freezing: the
seed fails `go test`, the intended fix passes.

Prompt (English):

```text
Run the tests, find the bug in MungeCWD and fix it without changing tests or exported API. Tests must pass.
```

Check: the test file must be byte-identical to the frozen template
(`cmp`), `func MungeCWD` must still exist in `munge.go`, and
`GOTOOLCHAIN=local GOPROXY=off go test ./...` must pass in the trial
dir. The Go flags guarantee the check makes no network calls.

### task3-tooling

Fixture: `bench/tasks/task3-tooling/fixture/` with exactly 12 regular
files scattered across 6 subdirectories (`README.md`, `data/` x3,
`src/` x2, `src/module/` x2, `assets/` x2, `scripts/` x1, `docs/` x2).
No dotfiles, no symlinks.

Prompt (English):

```text
Create a shell script tools/count.sh that prints the number of regular files under the current directory recursively, make it executable, run it and report the number.
```

The count.sh script itself is a regular file, so after a correct run
the trial dir holds 13 regular files. Check: `tools/count.sh` exists
and is executable, its reported number equals the independent
`find . -type f | wc -l` ground truth, and that ground truth equals the
expected 13 (12 seeded files plus the script). Extra files created by
the harness therefore fail the check.

## Pi CLI flags (verified against `pi --help`, version 0.84.2)

Relevant findings:

- `--print, -p`: non-interactive mode, process the prompt and exit.
  This is the flag used for task runs. Confirmed working.
- `--model <pattern>`: model pattern or ID, supports `provider/id`
  syntax. `pi --model anthropic/claude-sonnet-4.5` works and is used
  for model alignment.
- Session flags: `--session <path|id>`, `--session-id <id>`,
  `--session-dir <dir>`, `--no-session`. The runner uses
  `--session-dir` for isolation.
- Discovery flags: `--no-extensions`, `--no-skills`, `--no-themes`,
  `--no-prompt-templates`, `--no-context-files`. The runner passes all
  five so neither harness loads extensions, skills, themes, prompt
  templates, or workspace context files.
- `--offline`: disables startup network operations, but a probe showed
  `pi --offline -p "<prompt>"` produces an empty transcript. The
  benchmark therefore does not use `--offline` in task runs.
- `--version, -v`: prints the version. Used for the startup metric.
  A timing probe showed no measurable difference with or without the
  discovery flags, and none with `--offline`.

## Results

### Startup latency (ms)

Measured with 10 warmups plus 50 measured runs per harness (reduced from
the default 100 for this first run series; methodology otherwise
unchanged). smidja binary built with `CGO_ENABLED=0`, `-trimpath`,
`-ldflags "-s -w"`: static ELF, 6443170 bytes.

| Harness | Median | p95 | Runs | Date |
| --- | --- | --- | --- | --- |
| smidja | 1.49 | 1.72 | 50 | 2026-08-25 |
| pi | 584.5 | 627 | 50 | 2026-08-25 |

### Idle RSS (KB)

The automated `metrics.sh` idle loop produced 0 live samples for both
harnesses in this run because it reuses the startup arguments (for
smidja, `--version`) for the idle phase too, so the process exits before
sampling. The orchestrator therefore measured idle RSS manually with the
same procedure and command templates documented above: one process per
harness, stdin held open on a FIFO, 2 second settle, then 10 tree-RSS
samples at 1 second intervals. All samples were stable per harness.

| Harness | Median | p95 | Live samples | Date |
| --- | --- | --- | --- | --- |
| smidja | 5364 | 5364 | 10/10 | 2026-08-25 |
| pi | 182496 | 182672 | 10/10 | 2026-08-25 |

smidja idles at roughly 34x less resident memory than the full pi
process tree (single static binary versus a Node.js runtime).

### Task runs

One trial per harness per task (trial 1), model pinned to
`anthropic/claude-sonnet-4.5` on both sides.

| Task | Harness | Trial | Wall ms | Exit | Timed out | Check | Detail |
| --- | --- | --- | --- | --- | --- | --- | --- |
| task1 | smidja | 1 | 12160 | 0 | no | FAIL* | content correct: module, files, lines=13 all match ground truth; checker failed only on pretty-printed multi-line JSON |
| task1 | pi | 1 | 9345 | 0 | no | PASS | module, files, lines=13 match |
| task2 | smidja | 1 | 26356 | 0 | no | PASS | tests unchanged, API present, go test rc=0 |
| task2 | pi | 1 | 29431 | 0 | no | PASS | tests unchanged, API present, go test rc=0 |
| task3 | smidja | 1 | 19843 | 0 | no | PASS | script ok, reported 13 of expected 13 |
| task3 | pi | 1 | 12293 | 0 | no | PASS | script ok, reported 13 of expected 13 |

\* task1-smidja-1 is a checker artifact, not an agent failure: the JSON
extracted from the transcript contains exactly the correct values but is
pretty-printed across multiple lines and the strict single-pass jq
diff rejected the formatting. Both harnesses answered correctly.

## Limitations

- Tool surface differs between harnesses: smidja exposes read, write,
  edit, and exec tools; pi in this configuration exposes read, bash,
  edit, and write tools. The two harnesses are not tool-identical, so
  task difficulty is not perfectly matched.
- No extensions are loaded in either harness: smidja has no extension
  mechanism, and pi is run with the five `--no-*` discovery flags. The
  benchmark does not cover extension-loaded behavior.
- Single machine: all measurements come from one machine at one time;
  cross-machine variation (CPU, memory, network latency to the model
  provider, API key, model routing) is not controlled.
- Startup latency includes the shell's fork and exec overhead and any
  per-run variance of the machine; it does not include model round
  trips.
- Idle RSS samples where the process exited before the sampling wait
  are recorded as 0 KB and counted in `idle_live_samples`; a low live
  count flags harness startup instability.
- Task run wall time includes the full model round trip and any retries
  inside the harness, so it depends on provider latency and rate
  limits, not only on harness quality.
- smidja's exec tool has a 30 second per-command timeout
  (`SMIDJA_EXEC_TIMEOUT_SECS` default); a cold `go test` compile inside
  a trial is expected to fit, but very slow machines could hit it.
- One trial per harness per task in this first series. The planner
  baseline asks for repeated paired trials with alternating order;
  treat these numbers as indicative spike evidence, not as a settled
  ranking. Wall-time deltas between harnesses are within provider
  variance for tasks this small.
- `bench/metrics.sh` idle phase currently inherits the startup
  arguments and under-reports live samples for both harnesses (see the
  note under Idle RSS); the manual procedure documented there produced
  the recorded numbers. Fixing the script to take separate idle
  arguments is pending bench tooling work.
