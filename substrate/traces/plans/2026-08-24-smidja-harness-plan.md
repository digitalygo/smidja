---
document_type: mycelium-plan
plan_id: 2026-08-24-smidja-harness-plan
status: in-progress
created_at: 2026-08-24
planner: hermes-decision-draft
baseline_version: 1
execution_owner: orchestrator
execution_started_at: 2026-08-25T00:05:00+02:00
last_updated_at: 2026-09-05T15:26:32+02:00
---

# Piano Smidja harness

## Current execution snapshot

- **Status:** In progress. Fasi 0-4 completed and gated; all Fase 5 technical ecosystem deliverables completed and gated, with external creator acceptance pending.
- **Baseline identity:** `2026-08-24-smidja-harness-plan`, baseline version 1, planner handoff date 2026-08-24.
- **Execution baseline:** Started 2026-08-25T00:05:00+02:00, repository baseline commit b5cd629; Fase 4 review base a08ea0b and closed target 7184165; Fase 5 delivered `smidja-bundle-template` 83492b2, public registry `smidja-packages` 5c8ec5f and harness documentation through 94e4915.
- **Active phase:** Fase 5, ecosistema (real external creator acceptance).
- **Last verified checkpoint:** Checkpoint 2026-09-05T15:26:32+02:00, public package registry completed and gated.
- **Last successful checks:** Template and registry deterministic, quality, security and remote CI gates PASS; internal clean-room rehearsals and documentation human reviews PASS; both executable repositories have 95.1% statement coverage.
- **Open blockers:** The baseline completion criterion still requires a genuinely external creator to publish without Digitalygo assistance. Internal rehearsal is not external evidence.
- **Required approvals and gates:** All technical implementation approvals and gates are resolved. Closing or deferring the external-creator criterion requires real external evidence or explicit user approval.
- **Next action:** Have an external creator publish from the template or submit a content package to the registry without Digitalygo implementation assistance, then capture the clean-room evidence.

#### Checkpoint 2026-08-25T11:00:00+02:00: runaway guards removed, loop unbounded like Pi

- **Event:** Variation V-006 implemented and verified; Fase 1 design decisions continue.
- **Planner prediction:** Not in baseline (spike implementation detail).
- **Subagent claims:** go-dev removed MaxRounds/MaxToolCalls from internal/config (fields, constants, env overrides) and budget enforcement from internal/agent/loop.go (unbounded for loop exiting only on final stop, client error, or ctx cancellation); cli.go call sites updated; tests updated incl. TestRunTurnUnboundedRounds (50 consecutive toolUse turns then final answer succeeds).
- **Orchestrator finding:** Verified: go build/vet/gofmt clean, go test -count=1 ./... all packages ok, grep shows zero matches for MaxRounds/ErrMaxRounds/SMIDJA_MAX_ROUNDS/SMIDJA_MAX_TOOL_CALLS. The per-SSE-stream MaxToolCalls=32 cap in internal/openrouter/stream.go was intentionally KEPT: it is a protocol sanity/memory bound against malformed or hostile endpoints (security gate requirement), not a loop budget, and does not limit total tool calls across the turn.
- **Independently verified facts:** Full test suite green post-change; grep evidence as above.
- **Decision and impact:** Behavior now matches Pi: an unbounded tool loop burns tokens until user interrupt. Accepted explicitly by the user (variation V-006).
- **Next action:** Interface design questions to user; then delegate Fase 1 implementation.

#### Checkpoint 2026-08-25T11:30:00+02:00: stream tool-call cap removed; interface decisions collected

- **Event:** Variation V-007 implemented (per-stream MaxToolCalls cap removed from internal/openrouter); user answered the four interface questions.
- **Planner prediction:** Not in baseline.
- **Subagent claims:** go-dev removed defaultMaxToolCalls, exported MaxToolCalls var, errTooManyToolCalls sentinel and enforcement from stream.go; removed TestStreamTurnToolCallCountLimit; added TestStreamTurnManyToolCallsSucceed (40 distinct calls succeed). All caps retained: MaxStreamBytes 64 MiB cumulative, MaxTextBytes/MaxThinkingBytes 4 MiB, MaxToolArgsBytes 1 MiB per call, MaxEvents.
- **Orchestrator finding:** Verified: go build/vet/gofmt clean, full suite green, grep confirms zero traces of the cap in the package. Memory remains bounded by the retained cumulative caps; behavioral interruption becomes the core loop detector's responsibility.
- **Independently verified facts:** Full test suite green post-change.
- **Decision and impact:** User interface decisions recorded for Fase 1 implementation:
  1. Interface shape: option B - small per-group Go interfaces (e.g. ToolHook, LLMHook, SessionHook), extensions implement only what they need; event names kept identical to Pi for porting familiarity.
  2. Hook set: implement incrementally during development, starting with what Fase 1 needs (before_llm_call, after_llm_response, before_tool_call deny, after_tool_call, tool/command registration); session/gateway/stream hooks added when actually needed.
  3. Handler ordering: registration order of extensions, then registration order of handlers within each extension; no explicit priorities.
  4. Handler context API surface: question re-explained to user, answer pending.
- **Next action:** Await user decision on whether non-Pi protocol-level stream caps stay (Pi has none); then delegate Fase 1 implementation.

#### Checkpoint 2026-08-25T11:45:00+02:00: handler context API = full parity with Pi

- **Event:** User answered question 4: handler context must be complete like Pi's, no partial implementations.
- **Planner prediction:** Baseline hook surface section lists command registration and gateway events; architect proposal suggested minimal context.
- **Subagent claims:** None.
- **Orchestrator finding:** Pi extension context (dist/core/extensions/runner.js createContext + runtime actions) exposes registration APIs (registerCommand, registerProvider via modelRegistry), session actions (appendEntry), UI callbacks, and more; verified during source reading for the error-policy question.
- **Independently verified facts:** Source inspection of runner.js runtime wiring (lines ~150-215) shows appendEntry, modelRegistry.registerProvider/registerCommand-style actions wired into the extension context.
- **Decision and impact:** Smidja v0 extension context will expose the full action surface from the start: tool/command registration, session entry appending, UI callbacks, model registry access, matching Pi capability-for-capability. Recorded as variation V-008. Larger design surface for Fase 1; avoids rework when gateway/UI phases arrive.
- **Next action:** Pending user choice on protocol-level stream caps (keep as hardening vs remove for strict Pi parity); then delegate Fase 1 implementation.

#### Checkpoint 2026-08-25T12:00:00+02:00: memory limits aligned to Pi, protocol caps removed

- **Event:** Variation V-009 implemented and verified.
- **Planner prediction:** Not in baseline (spike hardening detail).
- **Subagent claims:** go-dev removed ALL protocol-level caps from internal/openrouter (MaxStreamBytes/countingReader, MaxTextBytes, MaxThinkingBytes, MaxToolArgsBytes, MaxEvents, sentinels, limit plumbing; stream_limits_test.go deleted; regression test with 100k events / 5 MiB text+thinking / 1.2 MiB args passes). internal/tools aligned to Pi: 2000 lines / 50 KB whichever-first truncation with Pi-style markers and full output in temp files (read and exec); limits configurable via Deps. config defaultMaxOutputBytes lowered to 50 KB.
- **Orchestrator finding:** Verified: go build/vet/gofmt clean; go test -count=1 ./... all packages ok. Flagged open item: write/edit 2 MiB input cap retained (outside the six table rows), awaiting user direction.
- **Independently verified facts:** Full suite green post-change; grep confirms removed cap symbols absent from code.
- **Decision and impact:** Smidja memory behavior now matches Pi exactly at all six points of the comparison table. Security posture regression vs spike hardening is explicit, user-approved, recorded in V-009 for future gates.
- **Next action:** Fase 1 design complete pending only write/edit input cap direction; then delegate Fase 1 implementation.

#### Checkpoint 2026-08-25T13:30:00+02:00: Wave 0 and Wave 1 of Fase 1 implemented

- **Event:** Fase 1 waves 0-1 implemented via delegated subagents; wave 2 (loop integration) starting.
- **Planner prediction:** Baseline steps 1-6 with frozen user decisions CP-011..CP-014.
- **Subagent claims:** solution-architect produced the detailed proposal (package layout sdk/ + internal/{extensions,contextmanager,subagent,retry,loopdetector,models,ui,sessionimport,update,buildinfo,content}, wave breakdown, verification matrix). Wave 0: sdk/ public contracts (option B interfaces, full-parity API surface, 102-capability parity matrix in docs/sdk-parity-matrix.md), agent ports, CLI root/chat split without behavior change. Wave 1 lanes: extensions registry/dispatcher (95.2% cov); session v3 codec for all nine entry types + opaque preservation + tree loader + sessionimport byte-exact/idempotent/conflict; contextmanager (double-criterion prune/compact, safety 0.95, pins, verbatim smidja-verbatim-v1 summaries, deterministic fallback) + subagent selector; retry exact Pi port with default MaxRetries=10 + IsContextOverflow; loopdetector ported from the user's own Pi extension ~/.pi/agent/extensions/loop-detector/index.ts preserving all defaults and six detectors; models registry + LineUI owning shared stdin; update client (checksums, atomic rename, locks) + buildinfo ldflags.
- **Orchestrator finding:** After every lane: go build/vet/gofmt clean and go test -count=1 ./... all green on the combined tree (18 packages). Notable verified details: session import validated byte-exact against a real local Pi session file incl. unmodeled fields; ports.go extended additively by wave 1C (LastUsageInput, EntryIDs, Compaction in ContextResult) documented in-file.
- **Independently verified facts:** Final combined run: all packages ok; -race green on extensions/ui/contextmanager/subagent per lane reports.
- **Decision and impact:** Digitalygo bundle packaging deferred: it lives in a second repository requiring user approval/location (architect flagged multi-repo gate); to be resolved before wave 3C.
- **Next action:** Wave 2: integrate context manager, hooks, retry, loop detector into internal/agent/loop.go under a single owner.

#### Checkpoint 2026-08-25T14:30:00+02:00: Waves 2-3 implemented, Fase 1 code complete pending bundle packaging

- **Event:** Loop integration (wave 2) and CLI composition (wave 3) implemented; live smoke successful.
- **Planner prediction:** Baseline steps 1-5 of Fase 1.
- **Subagent claims:** Wave 2: RunTurn v2 with Preparer/Hooks/Retry/Detector deps (nil-safe legacy preserved), context hook mutation chain, retry with auto-retry events and overflow sentinel not consuming retries, tool-call deny gate, detector warn->steering / block->run end with ErrLoopDetected, message_end before recording. Import cycles solved via documented mirror types in ports.go (host adapter pattern). Wave 3: LineUI owns shared stdin replacing local reader; contextmanager wired from new config fields (SMIDJA_CONTEXT etc., model registry lookup for context window); OpenRouterSelector; compaction entries persisted via session.AppendEntry; overflow recovery = forced safety compaction + one retry; subcommands import (--session-dir, stats, exit codes), update (--check/--version, buildinfo origin), version --json; root usage updated.
- **Orchestrator finding:** Wave 3 agent was interrupted during its final smoke but the work was complete on disk. Orchestrator verified: full suite green (18 packages), -race green on agent/cli/extensions, binary smoke: --version, version --json, bad-command usage, update --check clean failure path, and a LIVE end-to-end run with .env credentials through the complete new stack (contextmanager + detector + hooks registry + UI): file created and read back correctly.
- **Independently verified facts:** Commands and outputs listed above; all tests green post-interruption without further changes.
- **Decision and impact:** Fase 1 code complete except: Digitalygo bundle packaging (second repository - requires user approval/location per multi-repo gate) and golden compatibility fixtures vs installed Pi (wave 4). Quality and security gates for the Fase 1 delta not yet run.
- **Next action:** Commit/push ledger updates; resolve bundle repo location with user; wave 4 goldens.

#### Checkpoint 2026-08-25T17:30:00+02:00: wave 4 goldens complete, Fase 1 code closed

- **Event:** Golden compatibility fixtures and tests implemented; Fase 1 code work closed. Bundle packaging explicitly deferred by user ("la creiamo dopo prima finiamo l'harness di base").
- **Planner prediction:** Predicted verification for Fase 1: uso quotidiano del team e update deterministico tra due macchine — NOT yet satisfiable in-session; code-level scope otherwise complete.
- **Subagent claims:** go-dev produced sanitized committed fixture internal/session/testdata/pi-v3/session-basic.jsonl (70 lines, structural mirror of a real private session: 67 message + thinking_level_change + model_change; content fully synthetic); golden tests prove byte-exact entry round-trip incl. unmodeled fields (rawStopReason, details, thinkingSignature), tree/branch reconstruction, import byte-exactness/idempotence via CLI end-to-end; LOCAL validation against the real private file passed (byte-exact copy, idempotent re-import, loader clean) with no content committed (token-level privacy sweep); integration check SUCCEEDED in both directions: installed Pi 0.84.2 SessionManager reads smidja-written sessions (loadEntriesFromFile + buildContextEntries).
- **Orchestrator finding:** Full suite green (18 packages); commit 4100555 pushed; worktree clean. User provided an HTML export at tmp/ (gitignored): superseded by the original JSONL found in the local Pi store.
- **Independently verified facts:** Commands/results above; privacy sweep reported by agent (one probe script accidentally printed private fragments to stdout only; nothing written/staged/committed — noted as limitation).
- **Decision and impact:** Fase 1 status: CODE COMPLETE AND GATED (quality PASS direct mode, security PASS after corrections). Completion criterion "uso quotidiano del team solo con questo harness e update provato tra due macchine" remains OPEN by nature (requires human adoption over time + second machine). Bundle packaging deferred to post-base-harness per user decision (new variation V-011).
- **Next action:** Record V-011 + operation record update; hand Fase 1 completion criterion to real-world usage; remaining plan phases unchanged.

#### Checkpoint 2026-08-25T16:30:00+02:00: quality and security gates PASSED on Fase 1 delta

- **Event:** Both gates completed on the full Fase 1 delta after correction cycles.
- **Planner prediction:** Not predicted (gate workflow).
- **Subagent claims:** Quality: dedicated reviewer FAILed twice on irreproducible measurements (coverage ±0.3% variance across environments, artifact-hash mismatch never reproduced orchestrator-side) plus one REAL finding (sessionimport check-then-rename race). Security: dedicated reviewer BLOCKED with 5 real findings across two rounds: (H) tool-policy argument patches never reached execution; (M) imported session id/timestamp path traversal; (M) model-controlled detector text promoted to user-role steer message; (M) terminal control-char injection via import output; (M) stale batch authorization for stateful policies + (M) durable record kept pre-hook args.
- **Orchestrator finding:** All findings fixed via delegated corrections: link(2) atomic no-replace import commit (own commit); FinalArgs flows to execution/recording/detection with json.Valid validation; SessionFileName id/timestamp validation + containment guard shared by writer and importer; fixed host-owned steer templates with provenance prefix; sanitizeTerm quoting in cli output; durable record carries post-hook FinalArgs (apply before AppendAssistant); per-execution re-validation dispatch for stateful policies (two-pass model documented). Quality gate completed in DIRECT mode per quality-gate skill after repeated dedicated-measurement nondeterminism: artifact hash reproduced twice at clean HEAD, canonical suite green 18/18, coverage manifest frozen from canonical ./... run, rule-honesty sampled one file per area.
- **Independently verified facts:** Final security re-review PASS at artifact ba7de9a7d8a1bce32e947cd4e33f0597b3b129ca0ebccc3414e704ad7871c09b (range 55f3365..HEAD). Full suite green after every fix; each fix committed separately with regression tests incl. mutation checks (stash-revert proof for re-validation tests).
- **Decision and impact:** Fase 1 delta has explicit quality PASS (direct mode, cursor ba7de9a7 range) and security PASS. Remaining Fase 1 items: Digitalygo bundle packaging (second repo, user gate) and wave-4 golden compat fixtures.
- **Next action:** Commit/push ledger updates; resolve bundle repo location with user; wave 4 goldens.

#### Checkpoint 2026-08-25T12:15:00+02:00: write/edit input cap removed, Fase 1 design decisions complete

- **Event:** Variation V-010 implemented and verified; the last open design flag is closed.
- **Planner prediction:** Not in baseline.
- **Subagent claims:** go-dev removed writeMaxBytes and the oversize rejection from internal/tools; added TestWriteAcceptsLargeContent (3 MiB write succeeds); schema description updated.
- **Orchestrator finding:** Verified: go build/vet/gofmt clean, full suite green, grep confirms writeMaxBytes gone. Pi source verified to have no write/edit input limits.
- **Independently verified facts:** Full suite green post-change.
- **Decision and impact:** All memory/size limits in smidja now match Pi exactly. The complete Fase 1 decision set is collected: option B interfaces, incremental hooks, registration ordering, full-parity context API, retry identical to Pi with default 10, error policy from Pi, context management as core, loop detector as core, unbounded loop, all limits Pi-aligned.
- **Next action:** Delegate detailed Fase 1 implementation plan (hook registry + retry + error policy + full-parity ctx + core context management + loop detector) via solution-architect, then implement in waves.

#### Checkpoint 2026-08-25T15:30:00+02:00: quality gate PASS on Fase 1 delta (direct mode)

- **Event:** Quality gate completed on the full Fase 1 delta; cursor advanced to artifact 3e07dd06d4b0b4bbe90e78e1775c0b130c7148faf1e0a3cd865cf56aeeb147e8.
- **Planner prediction:** Not predicted (gate workflow).
- **Subagent claims:** Dedicated quality-gate runs: resubmission 1 FAIL with one REAL finding (sessionimport check-then-rename race, fixed via link(2) atomic no-replace commit, committed as its own commit) plus package-metadata gaps (binary-diff artifact form, per-file numstat, coverage numbers); resubmissions 2-3 FAILed only on irreproducible measurements (coverage ±0.3% variance between environments; artifact hash mismatch never reproduced orchestrator-side).
- **Orchestrator finding:** Switched to direct review per the quality-gate skill after repeated irreproducible dedicated measurements. All bounded checks pass: artifact hash reproduced twice at HEAD 1fa2b04 clean worktree; canonical verification green (build/vet/gofmt, 18/18 packages); coverage manifest frozen from canonical ./... run (openrouter 89.7, tools 86.3, others as listed); rule-honesty samples verified one file per area (retry patterns vs Pi source, contextmanager Validate() exact constraints, import_linux.go link(2) race fix, sdk/hooks.go option B shape with Pi ordering, loader compaction algorithm, dispatcher deny-only-via-Block, loopdetector ported defaults).
- **Independently verified facts:** Commands and outputs above; worktree clean at HEAD.
- **Decision and impact:** Quality gate PASSED in direct mode after the real finding was fixed. Note for future gates: dedicated-subagent measurement nondeterminism (hash/coverage) observed twice; direct mode used with identical contract and frozen literal outputs.
- **Next action:** Cumulative security gate on the Fase 1 delta.

## Planner baseline

### Problem statement

#### Metadati della bozza

- **Planner prediction:** Data: 2026-08-23 (rev. 3).
- **Planner prediction:** Stato: bozza per discussione col team.
- **Planner prediction:** Origine: trascrizione vocale del 2026-08-23 (`~/Downloads/kdrive/vocale-17min-trascrizione.txt`), verifica online e validazione multi-agente condotta da Hermes.

#### Obiettivo

Smiðja significa "forgia" in antico norreno: la forgia del codice e dell'IA. Per binario e cartelle vale la forma ASCII `smidja`.

Un harness agentico proprietario, clone concettuale di Pi (oggi `earendil-works/pi`, ex badlogic/pi-mono), scritto in Go, distribuito come binario singolo senza dipendenze. Il progetto è interamente MIT.

La caratteristica distintiva non è l'harness in sé ma la distribuzione. Un "pacchetto" è una build del binario con skill, agenti, subagenti, estensioni e prompt già dentro. Chi installa il pacchetto di qualcuno ha un harness nato con quel contenuto. Aggiornare significa aggiornare il binario, non sincronizzare file sparsi.

#### Perché

- Il sync attuale via chezmoi tra colleghi funziona ma non è deterministico: dipende dal fatto che l'agente esegua un comando. Un push che diventa un pacchetto che tutti aggiornano è un'altra cosa.
- Pi dimostra che il modello "harness minimo, tutto il resto estensione" è giusto. Ma è TypeScript su Node: runtime pesante, dipendenze fragili, installazione che richiede l'ecosistema npm.
- Il valore del setup Digitalygo sta nel framework di lavoro (skill, prompt, agenti, regole). Metterlo dentro la build elimina ogni frizione di installazione e porting, anche per il singolo developer con fisso e portatile.
- Le estensioni di gestione contesto che abbiamo scritto per Pi (tool prune, compact per selezione verbatim) diventano comportamento nativo configurabile invece di codice esterno da mantenere allineato.

### Research and evidence

Le evidenze elencate sotto sono state verificate il 2026-08-23 dall'agente di drafting Hermes. Le conclusioni operative ricavate dalla bozza restano planner prediction fino a verifica esecutiva.

- **Evidenza verificata il 2026-08-23 da Hermes:** Codex CLI Going Native, motivazioni dichiarate del rewrite da TypeScript: https://github.com/openai/codex/discussions/1174
- **Evidenza verificata il 2026-08-23 da Hermes:** Import iniziale codex-rs con rationale (binari standalone, sandbox nativa, niente GC): https://github.com/openai/codex/pull/629
- **Evidenza verificata il 2026-08-23 da Hermes:** SDK Go ufficiale OpenAI (rilasci settimanali attivi, v3.x agosto 2026): https://github.com/openai/openai-go
- **Evidenza verificata il 2026-08-23 da Hermes:** SDK Go ufficiale Anthropic: https://github.com/anthropics/anthropic-sdk-go
- **Evidenza verificata il 2026-08-23 da Hermes:** wazero, runtime WASM puro Go zero dipendenze: https://github.com/tetratelabs/wazero
- **Evidenza verificata il 2026-08-23 da Hermes:** Bot framework Telegram per Go maturo: https://github.com/go-telegram/bot
- **Evidenza verificata il 2026-08-23 da Hermes:** Coding agent esistente in Go (Charmbracelet Crush): https://github.com/charmbracelet/crush
- **Evidenza verificata il 2026-08-23 da Hermes:** Limiti del caricamento dinamico nativo Go (motivo della scelta D3): https://pkg.go.dev/plugin
- **Evidenza verificata il 2026-08-23 da Hermes:** Documento estensioni Pi: https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md
- **Evidenza verificata il 2026-08-23 da Hermes:** Standard AGENTS.md: https://agents.md/

### Hypotheses, decisions, and rationale

#### D1. Linguaggio: Go

Scartati TypeScript/Node per requisito (zero dipendenze, binario statico) e Rust dopo confronto:

- Il carico è orchestrazione I/O (streaming API, subprocess, file), non calcolo. Il vantaggio Rust sul controllo di memoria non cambia nulla qui, il costo di apprendimento sì: nessuno nel team lo conosce.
- Cross-compilazione Go banale (`GOOS`/`GOARCH`, `CGO_ENABLED=0`), binari statici da subito su tutte le piattaforme.
- Ecosistema pronto: SDK ufficiali `openai-go` e `anthropic-sdk-go`, bot Telegram maturi (`go-telegram/bot`), TUI provata (Charmbracelet, autore del coding agent Crush in Go), runtime WASM puro-Go (wazero) disponibile se servirà.
- Precedente di settore: OpenAI ha riscritto Codex CLI da TypeScript a nativo dichiarando le nostre identiche ragioni. Vedi evidenze in fondo.

#### D2. Licenza e modello di distribuzione

- Tutto MIT. Nessuna build centrale: ogni repository fa la sua build. La repo dell'harness è la base scarna stile Pi. La repo Digitalygo è un progetto che dipende dall'harness e aggiunge i nostri contenuti e workflow.
- Composizione per catena di fork: parti da una repo, forki, aggiungi, togli, buildi. Fork del fork va bene uguale. Chi mette codice dentro la propria build se la assume: è la stessa fiducia di qualsiasi software installato.
- Canali: GitHub Releases per tutti i pacchetti, Homebrew per l'harness base (e tap per chi vuole). Niente App Store, niente notarizzazione obbligatoria.
- Non obiettivo ora: manifest con `includes` verso altri pacchetti. La catena di fork copre il caso. Si rivaluta se l'ecosistema lo chiede.

#### D3. Estensioni: codice Go compilato a build time

- Un'estensione è un package Go conforme all'interfaccia di estensione del core, registrato nella build della propria repo. Niente caricamento dinamico nativo (il `plugin` package di Go è limitato a tre OS e fragile), niente ABI instabili.
- Conseguenza voluta: installare l'estensione di qualcuno significa usare (o forkare) il suo pacchetto, non scaricare un file dentro un binario esistente.
- Iterazione locale senza rebuild resta possibile sul layer dichiarativo (skill, prompt, config, definizioni agente come file MD), letto dal disco con precedenza sui baked-in.
- Opzioni future non bloccanti: subprocess JSON-RPC o WASM (wazero) per estensioni installabili senza ricompilare. Solo se emerge il caso d'uso reale.

#### D4. Contenuti baked-in: go:embed

Skill, agenti, subagenti, prompt e config di default finiscono nel binario con `embed.FS`. Feature nativa di Go, zero infrastruttura extra. Se serve accessorio da filesystem, estrazione read-only in cache versionata.

#### D5. Piattaforme

- Sviluppo e prima stabilizzazione su Linux.
- Pipeline release con macOS subito dopo: costo marginale quasi zero (stessa toolchain, stessi artefatti). Senza Developer ID Apple l'utente fa il workaround di Gatekeeper al primo avvio; accettabile, si valuta il certificato quando c'è distribuzione vera.
- Windows: differita. La distribuzione decente vuole un certificato di firma e oggi manca la domanda. Gate: primo cliente reale su Windows.

#### D6. Aggiornamenti deterministici

- Ogni build stampa dentro il binario origine, versione e commit (ldflags).
- `update` interroga le release della repo di origine, verifica checksum, sostituisce in modo atomico. Rollback = reinstallare la versione precedente.
- Contratto di compatibilità: ogni pacchetto dichiara la versione minima di harness richiesta.
- Update tocca solo ciò che è baked-in. Mai memory, sessioni, config utente.

#### D7. Cartelle e precedenze

Principio: gli standard esistenti vengono consumati automaticamente e restano file normali (leggibili e modificabili dall'agente); lo stato dinamico vive in un namespace privato; i baked-in sono immutabili.

- `AGENTS.md` nelle repo: caricato automaticamente come contesto quando si opera nell'albero che lo contiene. È un file normale: l'agente può modificarlo quando ha motivo per farlo.
- Skill standard in `~/.agents/skills/`: consumate automaticamente se esistono. Anche qui sono file normali, modificabili. Il contenuto che l'agente crea dinamicamente (nuove skill, memory) va di default nel namespace privato per non sporcare le cartelle condivise con altri strumenti, salvo configurazione diversa.
- Namespace privato `~/.smidja/`: config utente, memory, sessioni, skill create dinamicamente dall'agente, cache di estrazione baked-in. È la casa di scrittura dell'harness, mai toccata dagli update.
- Workspace `<repo>/.smidja/`: override e skill locali di progetto. Alternativa generica `.agent/`, si chiude in Fase 0.
- Ordine di precedenza delle fonti di contenuto: workspace della repo > baked-in del pacchetto > skill dinamiche in `~/.smidja/` > `~/.agents/` > default del core.
- Il workspace vince su tutto perché è specifico della repo: `AGENTS.md`, `.smidja/` di progetto ed eventuali skill standard presenti nella repo.
- Il baked-in viene dopo solo al workspace: chi distribuisce un pacchetto definisce il modo ufficiale di lavorare, e le regole per-repo restano l'unico modo di discostarsi. Un'azienda si pacchettizza agenti, skill e regole e ha già tutto; al massimo aggiunge override puntuali nelle singole repo.
- Le skill che l'agente crea dinamicamente vivono in `~/.smidja/skills/`: battono la collezione generica (un override fatto tramite l'agente è più recente e intenzionale) ma non il pacchetto.
- `~/.agents/` viene consumata con priorità inferiore al pacchetto: chi arriva da altri strumenti mantiene intatta la propria collezione e Smiðja la usa dove il pacchetto non dice nulla di diverso. Compatibilità con lo standard senza cedere il controllo del workflow.
- Due casi limite che l'ordine copre: l'azienda pacchettizzata (baked-in governa, override per-repo dove serve) e l'utente singolo senza pacchetto (base scarna: di fatto governano le sue skill in `~/.agents/` e i default del core).

#### Architettura

Componenti, in ordine di costruzione:

1. Loop agentico: sessioni, messaggi, streaming dai provider, chiamate tool.
2. Tool di base: file (read/write/edit), exec, grep/glob.
3. Sessioni persistenti su JSONL. Formato allineato a quello di Pi dove possibile, con comando di import delle sessioni Pi esistenti.
4. Interfaccia estensioni Go con superficie di hook ampia (vedi sotto).
5. Gestione contesto smart, baked-in nel pacchetto Digitalygo, parametri configurabili:
   - cache miss probabile dopo N ms dall'ultima richiesta;
   - tool prune: scatta quando il contesto supera la soglia `% prune` E sono già passati i ms di cache miss;
   - compact preventivo: stessa logica a doppio criterio con la sua soglia `% compact`;
   - compact di sicurezza: a una soglia alta (default ~95%, configurabile) scatta comunque, indipendentemente dai ms, perché se il prune preventivo non è mai partito il contesto può essersi riempito;
   - il prune sostituisce l'output dei tool vecchi con placeholder "rigira il tool", preserva gli ultimi M messaggi, consente il pin delle call critiche;
   - il compact non è un riassunto LLM: un subagent seleziona i messaggi da tenere e quelli restano verbatim.
6. Provider: al giorno 0 solo OpenRouter, una sola integrazione che copre comunque ogni modello. A MVP completato si aggiungono gli altri: Anthropic (API + abbonamento Pro/Max), OpenAI (API + accesso tipo Codex), DeepSeek, Alibaba (piano token Qwen), Moonshot AI (Kimi Coding Plan), Z.ai (Z.ai Coding Plan). L'astrazione provider dedicata resta rimandata a un ragionamento dedicato.
7. Packaging: embed dei contenuti, manifest di build, stamp versione.
8. Self-update (D6).
9. CLI minimale. Niente TUI elaborata inizialmente.
10. Pacchetti opzionali: subagent, deny di comandi. Poi gateway Telegram, poi Discord.

#### Superficie hook dell'interfaccia estensioni

Il set v0 proposto in rev. 1 era troppo povero. Gruppi di eventi da prevedere nel registro hook (estendibile senza rompere i pacchetti esistenti):

- sessione: start, end, resume, switch;
- ciclo LLM: before_llm_call (ultima chance di mutare messaggi e parametri), after_llm_response, eventi di stream;
- tool: before_tool_call (qui si innesta anche il deny), after_tool_call, errore/retry del tool;
- contesto: assembly del contesto, candidati prune, selezione compact, transform finale prima dell'invio;
- comandi: registrazione comandi custom;
- gateway: messaggio in entrata, risposta in uscita, cambio sessione remota.

Il registro è un enum/versione estensibile: aggiungere hook in sviluppo non è breaking change per le estensioni già scritte.

#### Rischi

- Manutenzione di un harness proprio è un impegno continuo. Mitigazione: scope minimo rigoroso, niente TUI elaborata, osservare le scelte di Pi e di Codex CLI invece di anticiparle tutte.
- Ogni modifica ai contenuti baked-in diventa una release del binario. Mitigazione: layer locale con precedenza per iterare, modalità dev che rilegge i file dal disco, release automatizzate.
- Gli accessi via abbonamento (Pro/Max, Codex, coding plan) hanno flussi OAuth diversi per ogni fornitore e possono cambiando senza preavviso: è lavoro per-integratore da mantenere. Mitigazione: isolare l'autenticazione dal resto dell'integrazione.
- Parità con l'upstream Pi che evolve veloce. Non è un obiettivo: la divergenza mirata (contesto smart nativo, packaging) è il punto.
- Provider minori senza SDK Go: client HTTP+SSE diretto, semplice, non blocca.

#### Non obiettivi

App Store e notarizzazione. Windows iniziale. Plugin WASM e subprocess. Server multi-tenant: ogni istanza gira sulla macchina locale. Astrazione formale dei provider, rimandata a un ragionamento dedicato.

#### Domande aperte

- Hook: conferma della lista per gruppo ed eventuali eventi extra che vuoi fin da subito.
- Import sessioni Pi: confermato desiderabile; verificare in Fase 0 quanto il formato JSONL di Pi sia stabile da allinearsi.

### Planned phases

Solo task, nessuna stima di giorni.

#### Fase 0, spike

- **Objective:** Planner prediction: Prototipo Go che valida peso e velocità del binario e la produttività del team sul linguaggio.
- **Planner predictions:** Il draft non predice file specifici per questa fase. Predice un prototipo Go con loop agentico minimo, streaming OpenRouter incluso, tool exec e file, sessioni JSONL, misure RAM a riposo, tempo di startup, dimensione binario e confronto diretto con Pi sugli stessi task.
- **Proposed steps:**
  1. Loop agentico minimo su OpenRouter, streaming incluso.
  2. Tool exec e file, sessioni su JSONL.
  3. Misura RAM a riposo, tempo di startup, dimensione binario, confronto diretto con Pi sugli stessi task.
- **Predicted verification:** Planner prediction: La verifica prevista è una demo CLI funzionante su un task reale e la raccolta dei numeri a confronto con Pi sugli stessi task.
- **Completion criterion:** Uscita: demo CLI funzionante su un task reale con i numeri a confronto.

#### Fase 1, MVP interno

- **Objective:** Planner prediction: Rendere Smidja utilizzabile quotidianamente dal team con gestione contesto smart, estensioni, packaging Digitalygo e update deterministico.
- **Planner predictions:** Il draft non predice file specifici per questa fase. Predice gestione contesto smart completa, registro hook, provider solo OpenRouter per l'intero MVP, sessioni JSONL compatibili Pi, comando import, contenuti Digitalygo baked-in e self-update.
- **Proposed steps:**
  1. Gestione contesto smart completa: doppio criterio prune/compact, compact di sicurezza, pin, selezione verbatim via subagent, parametri in config.
  2. Interfaccia estensioni con il registro hook descritto.
  3. Provider: solo OpenRouter per l'intero MVP.
  4. Sessioni JSONL compatibili Pi e comando di import.
  5. Packaging della repo Digitalygo con i nostri contenuti baked-in.
  6. Self-update.
- **Predicted verification:** Planner prediction: La verifica prevista è uso quotidiano del team solo con questo harness e prova dell'update deterministico tra almeno due macchine.
- **Completion criterion:** Uscita: il team lavora quotidianamente solo con questo harness, update deterministico provato tra almeno due macchine.

#### Fase 2, distribuzione

- **Objective:** Planner prediction: Preparare installazione e uso esterno dei pacchetti Smidja senza intervento del team Digitalygo.
- **Planner predictions:** Il draft non predice file specifici per questa fase. Predice provider estesi post-MVP, pipeline release Linux amd64/arm64 e macOS, Brew tap dell'harness base e repo-pacchetto pubblica come riferimento.
- **Proposed steps:**
  1. Provider estesi post-MVP: Anthropic (API + Pro/Max), OpenAI (API + Codex), DeepSeek, Alibaba Qwen token plan, Moonshot Kimi Coding Plan, Z.ai Coding Plan.
  2. Pipeline release Linux amd64/arm64 e macOS.
  3. Brew tap dell'harness base.
  4. Repo-pacchetto di esempio pubblica come riferimento.
- **Predicted verification:** Planner prediction: La verifica prevista è un'installazione esterna di un pacchetto e una sessione di lavoro senza richiesta di supporto al team.
- **Completion criterion:** Uscita: un esterno installa un pacchetto e lavora senza chiedere nulla a noi.

#### Fase 3, pacchetti opzionali

- **Objective:** Planner prediction: Separare subagent e deny di comandi in pacchetti opzionali, lasciandoli fuori dalla base scarna.
- **Planner predictions:** Il draft non predice file specifici per questa fase. Predice subagent come pacchetto opzionale e deny di comandi come pacchetto opzionale.
- **Proposed steps:**
  1. Subagent come pacchetto opzionale.
  2. Deny di comandi come pacchetto opzionale.
- **Predicted verification:** Planner prediction: La verifica prevista è che entrambi siano installabili scegliendo il pacchetto giusto e assenti nella base scarna.
- **Completion criterion:** Uscita: entrambi installabili scegliendo il pacchetto giusto, assenti nella base scarna.

#### Fase 4, gateway remoto

- **Objective:** Planner prediction: Abilitare lavoro remoto dal telefono con harness in esecuzione sul desktop.
- **Planner predictions:** Il draft non predice file specifici per questa fase. Predice gateway Telegram con le stesse primitive di sessione della CLI e Discord dopo.
- **Proposed steps:**
  1. Gateway Telegram con le stesse primitive di sessione della CLI.
  2. Discord dopo.
- **Predicted verification:** Planner prediction: La verifica prevista è una giornata di lavoro fatta dal telefono con l'harness che gira sul desktop.
- **Completion criterion:** Uscita: giornata di lavoro fatta dal telefono con l'harness che gira sul desktop.

#### Fase 5, ecosistema

- **Objective:** Planner prediction: Rendere autonomi i creatori esterni di pacchetti Smidja.
- **Planner predictions:** Il draft non predice file specifici per questa fase. Predice template repo per creare pacchetti, documentazione per i creatori di pacchetti ed eventuale supporto estensioni senza rebuild, subprocess o WASM, se c'è domanda.
- **Proposed steps:**
  1. Template repo per creare pacchetti.
  2. Documentazione per i creatori di pacchetti.
  3. Eventuale supporto estensioni senza rebuild (subprocess o WASM) se c'è domanda.
- **Predicted verification:** Planner prediction: La verifica prevista è pubblicazione autonoma di un pacchetto da parte di un creatore esterno.
- **Completion criterion:** Uscita: un creatore esterno pubblica il suo pacchetto in autonomia.

## Execution ledger

### Ledger rules

- I checkpoint di esecuzione sono append-only e vanno registrati solo per eventi materiali: validazione o rifiuto di ipotesi, cambi di scope o decisione, completamento fase verificato, check falliti, blocker, handoff, riprese, gate qualità o sicurezza, chiusura.
- Ogni checkpoint deve distinguere planner prediction, subagent claim, orchestrator finding e independently verified fact.
- Una fase resta aperta finché i controlli indipendenti previsti non passano. Una dichiarazione di subagent, un diff o un output non verificato non bastano per chiuderla.
- Le variazioni rispetto alla baseline vanno nella Plan-variation ledger senza riscrivere la baseline.
- Quality, security e operation record si registrano solo dopo l'avvio dell'esecuzione da parte dell'orchestrator.

### Fase 0, spike execution checkpoints

#### Checkpoint 2026-08-25T00:05:00+02:00: baseline validation and execution start

- **Event:** Execution of the plan starts; active phase Fase 0, spike.
- **Planner prediction:** Plan status ready-for-execution, schema-compliant, next action was orchestrator validation and baseline recording.
- **Subagent claims:** None.
- **Orchestrator finding:** The plan document follows the mycelium-plan schema (frontmatter, snapshot, planner baseline with all subsections, ledger rules, empty phase checkpoint sections, variation ledger, closure evidence). The repository contains only docs and substrate; no DRC-* or EXP-* files exist under substrate/, so no directive or expectation files apply beyond shared standards. The repository README confirms scope and principles consistent with the planner baseline.
- **Independently verified facts:** `git status` clean at commit b5cd629; plan file read in full; Pi session storage inspected locally at `~/.pi/agent/sessions/`: sessions are JSONL with header `{"type":"session","version":3,...}`, followed by typed entries (`model_change`, `thinking_level_change`, `message`) with `id`/`parentId` chaining; message entries carry role user/assistant/toolResult with content blocks (text, thinking, toolCall) and usage data. This anchors the Fase 0 JSONL alignment target on format version 3 as observed on the installed Pi client. Limitation: format stability over time still to be confirmed against upstream repo history.
- **Decision and impact:** Execution proceeds on Fase 0 without user input; open questions from the baseline (hook list confirmation, `.smidja/` vs `.agent/` workspace) remain gated to their planned resolution points inside Fase 0/Fase 1.
- **Next action:** Research and architecture proposal for the Fase 0 spike, then delegated implementation.

#### Checkpoint 2026-08-25T02:20:00+02:00: research complete and Fase 0 architecture decided

- **Event:** Research phase of Fase 0 completed; implementation architecture accepted; delegated implementation starts.
- **Planner prediction:** Fase 0 predicts a minimal agentic Go loop on OpenRouter with streaming, exec/file tools, JSONL sessions, and comparison measurements vs Pi; open questions: `.smidja/` vs `.agent/` to close in Fase 0, Pi JSONL stability to verify in Fase 0.
- **Subagent claims:** codebase-analyzer produced full Pi session format v3 specification from installed pi-coding-agent 0.84.2 source (entry types, message shapes, usage/cost shape, cwd-munged directory layout, append-only parentId tree). web-researcher produced the OpenRouter chat completions SSE contract (tool_calls delta accumulation by index, usage chunk with empty choices, error envelope also as SSE event after HTTP 200) with explicit caveat that live August 2026 verification was not possible in its sandbox.
- **Orchestrator finding:** Both reports are consistent with each other and with locally observed Pi sessions. solution-architect proposal reviewed and accepted: direct `net/http` SSE client with standard library only (no openai-go in Phase 0), Pi v3-aligned write-only session subset (session header, message entries user/assistant/toolResult, linear parentId chain), four tools read/write/edit/exec with containment limits, minimal line-oriented CLI with `-p` mode, benchmark methodology committed under `bench/` and `docs/benchmarks/phase-0.md`, module path github.com/digitalygo/smidja.
- **Independently verified facts:** Pi session format confirmed against local live sessions (`~/.pi/agent/sessions/--var-home-luca-Documents-github-digitalygo-smidja--/*.jsonl`, header version 3); `go version` = go1.26.6 linux/amd64 available; OpenRouter API key presence to be checked at demo time via environment variable only.
- **Decision and impact:** Workspace directory question closed within Fase 0 scope: `<repo>/.smidja/` wins over generic `.agent/` (rationale: matches `~/.smidja/` namespace ownership; avoids second generic namespace; `.agents` plural remains the interoperability standard). Pi JSONL alignment target frozen on format version 3 as observed in installed Pi 0.84.2; no import compatibility claimed yet (deferred to Fase 1 per plan). No deviation from planner baseline scope; no approval gate triggered.
- **Next action:** Wave 1 delegation: foundation contracts (go.mod, internal/config, internal/workspace, internal/agent conversation types and narrow interfaces).

#### Checkpoint 2026-08-25T03:00:00+02:00: Fase 0 implementation complete (waves 1-3 + bench harness)

- **Event:** All Fase 0 deliverables implemented and verified; phase candidate ready for gates.
- **Planner prediction:** Minimal agentic loop on OpenRouter with streaming, exec/file tools, JSONL sessions aligned to Pi, measurements of idle RAM / startup / binary size, comparison vs Pi on same tasks.
- **Subagent claims:** go-dev waves delivered: foundation contracts (go.mod, config, workspace, agent types/seams); openrouter SSE client with tool-call accumulation and tolerant decoders; session store with Pi v3 alignment (UUIDv7, 8-hex ids, lazy append-only writes); tools read/write/edit/exec with containment and caps; agent loop + CLI (-p/-model/-system/-version/REPL); bench harness (build.sh, metrics.sh, run-task.sh, three task fixtures) plus docs/benchmarks/phase-0.md.
- **Orchestrator finding:** Each wave verified by direct inspection: builds, vet, tests green after every wave. Live bug found in first real streaming run (OpenRouter usage.cost as bare number) and fixed via tolerant decoder with regression tests. Live end-to-end demo succeeded: model created hello.go and ran it through the exec tool; session JSONL verified Pi-v3-conformant (header version 3, parentId chain, usage/cost shape).
- **Independently verified facts:** CGO_ENABLED=0 stripped binary = static ELF 6,443,170 bytes. Startup median 1.49 ms (p95 1.72) vs pi 0.84.2 median 584.5 ms (p95 627). Idle RSS median 5,364 KB vs pi tree 182,496 KB (~34x less). Paired tasks (model anthropic/claude-sonnet-4.5 both sides): task1 content correct on both (smidja checker artifact on multi-line JSON documented), task2 PASS both (26.4s vs 29.4s), task3 PASS both (19.8s vs 12.3s). Results recorded in docs/benchmarks/phase-0.md.
- **Decision and impact:** Fase 0 completion criterion met: working CLI demo on a real task plus comparison numbers. Bench methodology deviation recorded as variation V-001 (single trial per pair) and V-002 (manual idle RSS procedure).
- **Next action:** Quality gate then cumulative security gate.

#### Checkpoint 2026-08-25T04:30:00+02:00: quality gate PASS

- **Event:** Dedicated quality-gate review completed; cursor advanced to final checkpoint hash ddd94a16278468ef3bde74824c5cd21627b82a5bd5ef8f9605e1b111024ccd3e.
- **Planner prediction:** Not predicted (gate process is orchestrator workflow, not baseline scope).
- **Subagent claims:** quality-gate subagent verdicts: attempt 1 FAIL (package metadata incomplete), attempt 2 FAIL (per-file manifest gaps, bench file count error, aggregate inconsistency), attempt 3 PASS (full-session package v3, hash 8161e0bf over 50 files); incremental rounds for security-driven deltas: one FAIL (non-canonical hash order), then PASS at each corrected freeze (b1713d7a, 338f213f, 002b5ab7, 01dfabe2, b47cf56e, ddd94a16).
- **Orchestrator finding:** All FAILs were package-metadata completeness issues or hash-canonicalization errors, never code defects. Final incremental package covers bench/run-task.sh only; full-session coverage evidence retained from package v3.
- **Independently verified facts:** Orchestrator re-ran before each submission: go build ./..., go vet ./..., gofmt -l . empty, go test -count=1 ./... all packages ok, bash -n clean, per-file wc -l, pure-delta sha256 recomputation matched each frozen claim. Coverage: agent 90.8%, cli 79.2%, config 83.9%, openrouter 90.0%, session 75.8%, tools 87.0%, workspace 83.8%; cmd/smidja 0% accepted (thin wiring).
- **Decision and impact:** Quality gate PASSED. No open quality findings.
- **Next action:** Cumulative security gate.

#### Checkpoint 2026-08-25T05:30:00+02:00: cumulative security gate PASS after correction cycles

- **Event:** Dedicated security-review-specialist review completed; final verdict PASS on cumulative package 064cd7a51e8f44d93baac56ef8d7517cce147b27c90693735da8286d501843fb (51 files).
- **Planner prediction:** Not predicted (gate process is orchestrator workflow, not baseline scope).
- **Subagent claims:** Security rounds: R1 BLOCKED (High: bench post-checks inherited OPENROUTER_API_KEY into model-influenced executions; Medium: unbounded SSE accumulation + quadratic concatenation) -> fixed. R2 BLOCKED (High: trust of model-writable control files/pidfile/env-via-PATH; Medium: per-block not cumulative text/thinking caps) -> fixed. R3 BLOCKED (High: re-exec env leak beyond *_API_KEY patterns; High: PATH shadowing of check toolchain; Medium: forgeable check.out verdict; Medium: group kill only on timeout) -> fixed. R4 BLOCKED (Medium: CHECK_OUTFILE path recoverable via /proc environ, unlink+replace forgery) -> fixed. R5 BLOCKED (Medium: pipe-hold descendant could stall substitution and inject forged last line) -> fixed. R6 BLOCKED (Medium: command-substitution pipe still reopenable via ancestor fd -> unbounded hang) -> fixed. R7 BLOCKED (Medium: DIAG FIFO swap pre-timeout block; post-check substitutions still capture pipes) -> fixed by removing the diagnostic channel entirely. R8 PASS with informational notes only.
- **Orchestrator finding:** The harness core (workspace containment, exec contract, secret stripping, session encoding, zero-dependency posture) was assessed clean from round 1 onward. All blocking findings concentrated in the bench runner script and were resolved by progressive simplification ending in: synchronous silent checker (literal /dev/null redirects), exit-code-only verdict, plain-assignment detail strings, no command substitutions in verdict/record path. Documented accepted residuals: exec tool is not a sandbox; same-UID setsid descendants may persist after timeout kill but can neither stall nor taint anything; benchmark record outputs remain same-UID-writable.
- **Independently verified facts:** Authoring-time adversarial suites (final: 31/31 incl. hold7 pipe-hold 34ms true verdict, fifo7 structural no-path confirmation, rc-124 timeout path, normal task1/2/3 passes); orchestrator re-ran build/vet/gofmt/tests/bash -n and recomputed both hashes before each submission. Live smoke against real OpenRouter succeeded after every stream.go change. staticcheck/shellcheck/govulncheck unavailable on machine (recorded limitation).
- **Decision and impact:** Security gate PASSED. One deliberate behavior change to bench output recorded as variation V-003: result.txt/results.tsv detail reduced to generic reason strings (full transcript remains in out.log).
- **Next action:** Close Fase 0; record operation record.

### Fase 1, MVP interno execution checkpoints

#### Checkpoint 2026-08-25T10:30:00+02:00: Fase 1 design decisions from user

- **Event:** User decisions collected for three Fase 1 design areas before implementation.
- **Planner prediction:** Baseline predicted smart context management baked into the Digitalygo package, extension interface with hook registry, OpenRouter-only provider.
- **Subagent claims:** None.
- **Orchestrator finding:** Pi source inspected (installed 0.84.2) to ground the decisions: retry policy in `pi-ai/dist/utils/retry.js` (regex classification retryable vs quota/billing non-retryable, exponential backoff baseDelayMs x 2^(n-1), agent-level not SDK-level, aborts never retried, context overflow excluded); extension error policy in `dist/core/extensions/runner.js` (per-handler try/catch, log-and-continue, blocking decisions via explicit return values not exceptions); agent loop in `pi-agent-core/dist/agent-loop.js` is `while (true)` with NO max-rounds or max-tool-call guards.
- **Independently verified facts:** Pi settings docs confirm retry defaults: enabled true, maxRetries 3, baseDelayMs 2000; user's own settings use maxRetries 20. Pi loop has no turn/tool budget constants (verified by reading agent-loop.js).
- **Decision and impact:** User decisions recorded:
  1. Retry copied from Pi verbatim (classification patterns, exponential agent-level backoff, events), but default maxRetries = 10 instead of Pi's 3; configurable as in Pi. Recorded as variation V-005.
  2. Extension error policy copied from Pi: per-handler isolation, errors logged and skipped, blocking decisions only via explicit return values (e.g. tool deny), never via panics/errors.
  3. Extensions can register new tools, exactly like Pi, in addition to custom commands.
  4. Smart context management is CORE of smidja base, not a package-baked extension; packages configure parameters/content. Recorded as variation V-004.
  5. Noted divergence: smidja keeps maxRounds/maxToolCalls runaway guards absent in Pi (deliberate, documented in code).
- **Next action:** Interface design questions to user; then delegate Fase 1 implementation.

### Fase 2, distribuzione execution checkpoints

#### Checkpoint 2026-08-26T01:30:00+02:00: Fase 2 blocks 1-8 implemented and gated

- **Event:** All eight Fase 2 blocks implemented, verified, committed and pushed; quality (direct) and security gates PASSED on the delta.
- **Planner prediction:** Baseline Fase 2: provider estesi post-MVP, pipeline release Linux/macOS, brew tap, repo-pacchetto di esempio. Revised by V-012 (all-Pi providers minus bedrock/copilot/radius; architect corrections accepted).
- **Subagent claims:** Block 1 release pipeline (4 targets + checksums.txt matching internal/update, GH workflow); Block 2 sdk.Run composition seam with bundle validation/ConfigDefaults/Deps injection; Block 3 smidja-digitalygo bundle repo implemented+pushed (beca6a0, embed content, sample extension, CI publishing assets, pinned harness commit); Block 4 internal/providers core + authstore (facade suite unmodified); Block 5 anthropic-messages/gemini/openai-responses drivers ported from pi-ai; Block 6 32-provider manifest frozen with source citations; Block 7 OAuth flows for openrouter/anthropic/codex/xai/kimi (ports exact incl. callback ports and device flows); Block 8 auth login/logout/status CLI, -provider selection with refresh-in-produce-path, brew formula template + tap docs.
- **Orchestrator finding:** Every block verified before commit: build/vet/gofmt clean, full suite green (final: 25 packages), -race on touched packages. Gates on the Fase 2 delta (88 files, artifact d49c0661...): quality PASS direct mode per documented precedent; security PASS direct mode — storage/callbacks/status-output inspected clean; non-blocking hardening note: pin GitHub Actions by SHA in release workflows.
- **Independently verified facts:** Suite counts, gate inspections and commits listed above; both repos pushed (smidja alpha, smidja-digitalygo main).
- **Decision and impact:** Fase 2 code complete. Completion criterion (un esterno installa e lavora senza chiedere nulla) requires a real external user — deferred to post-development manual testing together with multi-machine update evidence (user decision).
- **Next action:** Brew tap repo creation when distribution starts; operation record update; remaining phases unchanged.

#### Checkpoint 2026-08-26T12:00:00+02:00: AGENTS.md compliance sweep, public release and working brew tap

- **Event:** Full AGENTS.md compliance enforced; harness published as v0.1.0; homebrew tap created and install verified end-to-end.
- **Planner prediction:** Baseline Fase 2 completion criterion: un esterno installa e lavora senza chiedere nulla.
- **Subagent claims:** compliance sweep removed 6,506 comment lines across 127 Go files (build directives preserved), split tools.go into per-tool files and oversized test files by behavior with zero dropped cases, raised internal/subagent coverage 70.7->100; browser-opener test isolated after user reported real browser tabs opening at https://127.0.0.1:1/ during test runs.
- **Orchestrator finding:** Zero-comment grep = 0; 25/25 packages green; all packages >=80% coverage. Repo digitalygo/smidja made public (user decision) since the private state blocked both the source-tarball formula and the external-install criterion. Tag v0.1.0 released via workflow on first attempt (4 binaries + checksums.txt). Tap digitalygo/homebrew-smidja created with source-based formula pinned to the public tarball sha256; local verification: brew tap -> install --build-from-source -> smidja v0.1.0 runs -> brew test green. Note: source builds report commit=none (tarballs carry no git metadata).
- **Independently verified facts:** Commands/outputs above; both repos pushed; worktree clean.
- **Decision and impact:** Fase 2 completion criterion now demonstrably satisfiable externally (public repo + release + brew). Remaining manual evidence: team daily use, multi-machine update, true external user.
- **Next action:** Post-development manual validation; Fasi 3-5 unchanged.

#### Checkpoint 2026-08-26T20:00:00+02:00: Fase 3 implemented and gated

- **Event:** All Fase 3 work streams implemented, validated, gated, committed and pushed on both repos.
- **Planner prediction:** Baseline steps 1-3 (pacchetto Digitalygo con contenuti reali, registry GitHub con validazione e firma opzionale, CLI gestione pacchetti) revised by V-013/V-014.
- **Subagent claims:** solution-architect validation produced the frozen design (V-014). Waves delivered: internal/mcp stdio bridge (92.6% cov, NDJSON auto + Content-Length fallback respawn, restart supervisor no-replay); internal/packages content-package core (87.4% cov, strict manifest, tree integrity, flat resolution with commit pinning, atomic staged store); bundle repo ports of youtrack/exa/mistralocr/replicate as proper RegisterToolHooks extensions via real sdk.API ToolCatalog (bundle commit 612f3ef pushed, harness pinned at 2fcacd2 then repinned implicitly by go.mod bump); harness integration wave: extensions/api.go real SDK API (98.8% cov), MCP runtime with fail-closed workspace trust, skills catalog + /skill command, full smidja pkg CLI tree with GitHub codeload fetch; AGENTS.md compliance sweep (dc89aef) preceding the phase.
- **Orchestrator finding:** Security gate PASS (attempt 2 bounded review) on artifact 672c8ace... covering the delta; quality in direct mode per precedent: zero comments repo-wide verified again post-integration, suite green throughout, -race clean on touched packages. One known flaky timing test flagged (internal/mcp TestListToolsRetryOnceAfterRestart) pre-existing.
- **Independently verified facts:** git log sequence above; 28 packages green at close; both repos clean and pushed.
- **Decision and impact:** Fase 3 code complete. Completion criterion "il team installa e disinstalla pacchetti con un comando" requires real team usage (post-development). Registry index repo digitalygo/smidja-packages not yet created (needed only when first third-party package is published or when the Digitalygo package itself is distributed via pkg install).
- **Next action:** Create digitalygo/smidja-packages index when needed; populate bundle with real final content iteratively during daily use; Fase 4 gateway when user says go.

### Fase 3, pacchetti opzionali execution checkpoints

### Fase 4, gateway remoto execution checkpoints

#### Checkpoint 2026-09-04T14:09:25+02:00: Fase 4 resumed and closure gaps audited

- **Event:** Fase 4 execution resumed from a stale ledger after its implementation commits had landed without phase checkpoints or gate evidence.
- **Planner prediction:** Baseline Fase 4 predicted Telegram and later Discord with remote-session parity; V-015 revised the approved scope to Telegram plus web, cache-preserving session reopen, Pi-aligned content and model loading, deterministic resume digest, rich Telegram output, gateway durability, and four implementation waves ending in integration and gates.
- **Subagent claims:** Traces and codebase locators delimited the historical delta as a08ea0b..5e7e364. Analyzer and solution-architect audits found the gateway kernel, Telegram/web transports, session continuity, content/model loading and credential login present, but identified unclosed V-015 requirements and one repository-rule violation.
- **Orchestrator finding:** Direct inspection confirmed missing cross-process authstore RMW locking, absent per-driver request-prefix replay goldens, no persisted settings loader or user-facing models-catalog URL, ineffective cross-tier content precedence, missing bundle instruction/model wiring, absent stored web-token wiring, and a prose comment added under internal/session. Discord and the unfinished `smidja run` subcommand are outside the approved Fase 4 closure boundary.
- **Independently verified facts:** Default branch `alpha` was clean and synchronized at 5e7e36461cc70d9f8c13c1cb3a04819217225cff; the immutable Fase 4 review base is a08ea0b5f1e2cde5075e0db19a66843e80ce61e9. The historical delta contains five commits and 73 files. `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test -count=1 ./...` passed before corrections. Source inspection and repository searches confirmed the listed gaps.
- **Decision and impact:** Fase 4 remains in progress. The missing items are corrections within already approved V-015 scope, so no new behavior approval is required. The final quality and security reviews will cover the complete historical delta plus corrections from the Fase 3 closure base.
- **Next action:** Delegate the correction slices, rerun deterministic verification and delta coverage, then submit the same frozen artifact to quality and focused security review.

#### Checkpoint 2026-09-04T21:41:53+02:00: V-015 corrections complete and deterministic gate passed

- **Event:** The bounded Fase 4 correction package completed and its deterministic gate passed on the full historical phase delta.
- **Planner prediction:** V-015 required cross-process authstore RMW safety, strict settings and content/model parity, configurable catalog sourcing, cache-preserving reopen, stored gateway credentials, provider request-prefix fidelity, and integration gates.
- **Subagent claims:** Backend races selected the stronger authstore/web and settings/content implementations after independent verification and architect adjudication. A focused provider task added four replay-golden contracts and order-sensitive runtime-profile tests. A documentation task aligned README, auth and settings guidance.
- **Orchestrator finding:** Inspection of production files, new tests, fixtures and documentation confirmed that the audited V-015 gaps are closed. The final executable delta adds fresh-read locked auth mutations, stored web-token resolution, trusted persisted settings, canonical content precedence, rooted bundle instructions/models/settings, configured provider/retry/catalog wiring, request replay goldens for all four drivers, and tool-order-sensitive profile identity.
- **Independently verified facts:** Artifact target 7184165fe3599c343813e954a289d4b3080dc74a has tree 1b329e71f65fad3118eed4f2e413ecb20127432d, changed-file manifest hash 2c9d6c70469f28e928c002fd0a73dfdc9899edefa852caedff11ee1e738176f3, and binary diff hash 05b81207373af1aea4e0c7d2a610bf2c2af6b5cafd69f3d7b6c0ccfbcd8d57b1 from base a08ea0b. `git diff --check`, repository comment compliance, `go build ./...`, Linux and Darwin static cross-builds, targeted Windows builds for authstore/session, `go vet ./...`, `go test -count=1 ./...`, 20x auth concurrency tests, 20x provider replay goldens, 100x gateway cancellation test, race tests across all 15 delta packages, and aggregate delta-package coverage all passed; coverage was 89.2%. An extra unsupported full Windows build failed in unchanged `internal/tools/exec.go`; targeted changed lock packages compiled for Windows. A repeated MCP stress run exposed a pre-existing out-of-delta timing failure while the required full suite passed.
- **Decision and impact:** Fase 4 implementation and deterministic verification are complete. Artifact 05b81207 is frozen for both judgment gates; any correction will create a new artifact and invalidate prior verdicts.
- **Next action:** Run the dedicated quality judgment pass against artifact 05b81207, then the focused security review against the same artifact.

#### Checkpoint 2026-09-04T21:47:54+02:00: Fase 4 quality gate passed

- **Event:** The dedicated quality judgment completed on the frozen Fase 4 artifact.
- **Planner prediction:** Fase 4 required an integration gate after the Telegram/web, session, content and provider work; repository rules additionally require mapped tests, no weakened tests, no prose code comments, and 80-100% coverage on changed executable lines.
- **Subagent claims:** The quality-gate specialist returned exact verdict PASS after reproducing the tree, manifest and binary-diff hashes and inspecting the complete delta, tests and documentation.
- **Orchestrator finding:** The judgment covered a08ea0b..7184165 with living-plan edits excluded. It confirmed every V-015 acceptance surface, no deleted or weakened tests, cohesive file sizes, accurate documentation, and sufficient mapped coverage.
- **Independently verified facts:** The deterministic pass preceding judgment was PASS with aggregate coverage 89.2%. Quality artifact identifiers are target 7184165fe3599c343813e954a289d4b3080dc74a, tree 1b329e71f65fad3118eed4f2e413ecb20127432d, manifest 2c9d6c70469f28e928c002fd0a73dfdc9899edefa852caedff11ee1e738176f3, and binary diff 05b81207373af1aea4e0c7d2a610bf2c2af6b5cafd69f3d7b6c0ccfbcd8d57b1. Directives and expectations are absent. The only advisories are the unchanged unsupported full Windows build, the pre-existing out-of-delta MCP stress flake, and the 30-second stale-lock failure mode on non-Linux systems.
- **Decision and impact:** Quality gate PASS. Artifact 05b81207 advances unchanged to focused security review.
- **Next action:** Run focused security review on artifact 05b81207 with V-009 accepted-design posture preserved.

#### Checkpoint 2026-09-04T21:55:43+02:00: Fase 4 security gate passed

- **Event:** The focused security review completed on the unchanged frozen Fase 4 artifact.
- **Planner prediction:** Fase 4 required fail-closed gateway trust, local credential safety, durable session/journal handling, bounded remote inputs, and security review after integration; V-009 requires treating uncapped provider streams as accepted design.
- **Subagent claims:** The security-review specialist returned exact verdict PASS after source review of 46 production files and the relevant tests and fixtures.
- **Orchestrator finding:** The review covered authstore lock and atomic-write behavior, OAuth refresh concurrency, Telegram allowlists and token redaction, web loopback/auth/CSRF/origin/body limits, trusted settings and catalog SSRF boundaries, content/session containment, journal recovery and concurrency, and synthetic provider replay fixtures. No blocking finding remained.
- **Independently verified facts:** Security artifact identifiers match the quality artifact: target 7184165fe3599c343813e954a289d4b3080dc74a, tree 1b329e71f65fad3118eed4f2e413ecb20127432d, manifest 2c9d6c70469f28e928c002fd0a73dfdc9899edefa852caedff11ee1e738176f3, and binary diff 05b81207373af1aea4e0c7d2a610bf2c2af6b5cafd69f3d7b6c0ccfbcd8d57b1. Review mode was strict read-only and response-only. Hardening notes were non-blocking: root-symlink defense in depth, non-Linux stale lock availability, models-store directory mode consistency, web login throttling, stricter missing-Origin handling, future multi-user transcript filtering, and SSE client caps.
- **Decision and impact:** Security gate PASS. All technical Fase 4 work and gates are closed; only the real-world phone-workday completion criterion remains unresolved.
- **Next action:** Verify real phone-workday evidence or obtain explicit user approval to defer that adoption criterion, then append the Fase 4 completion checkpoint.

#### Checkpoint 2026-09-04T22:30:32+02:00: Fase 4 completed and gated

- **Event:** Fase 4 completed after the user explicitly approved deferring the real-world phone-workday criterion as a non-blocking adoption check.
- **Planner prediction:** Baseline Fase 4 predicted remote gateway operation and correct session resume; V-015 revised the approved implementation to Telegram plus web, Pi-aligned content/configuration, cache-preserving reopen, deterministic digest, rich Telegram output, durable concurrent sessions and complete gates.
- **Subagent claims:** Implementation agents reported all correction slices complete; quality-gate and security-review specialists returned PASS on the same frozen artifact.
- **Orchestrator finding:** Planned gateway work became `internal/gateway{,/telegram,/web}` plus CLI wiring. Actual supporting changes also span `internal/session`, `internal/content`, `internal/config`, `internal/models`, `internal/summary`, `internal/authstore`, provider replay tests and user documentation. Direct inspection found no unresolved V-015 implementation requirement.
- **Independently verified facts:** Artifact 05b81207373af1aea4e0c7d2a610bf2c2af6b5cafd69f3d7b6c0ccfbcd8d57b1 at target 7184165 passed the deterministic gate, dedicated quality judgment and focused security review. Coverage was 89.2%. Local journal metadata could not prove the baseline phone-workday criterion, and the user approved its explicit deferral on 2026-09-04. Technical limitations and non-blocking hardening notes remain recorded in the preceding gate checkpoints.
- **Decision and impact:** Fase 4 status is COMPLETED. The deferred phone-workday check remains post-phase adoption evidence and does not reopen implementation or gate status. Discord remains excluded, `smidja run` remains outside this phase, and the overall plan stays in progress for Fase 5.
- **Next action:** Activate the Fase 5 handoff with its scope reconciled against V-013.

### Fase 5, ecosistema execution checkpoints

#### Checkpoint 2026-09-04T22:30:33+02:00: Fase 5 handoff after Fase 4 closure

- **Event:** Fase 5 became the active phase; no Fase 5 implementation started.
- **Planner prediction:** The baseline proposed a package-template repository, package-creator documentation, optional no-rebuild extensions if demanded, and autonomous publication by an external creator.
- **Subagent claims:** Earlier traces/codebase audits found no package-template repository or creator guide in this repository and recorded the package index as not yet created.
- **Orchestrator finding:** V-013 permanently ruled out subprocess, WASM, Go plugins and every other no-rebuild extension mechanism. The remaining phase is a template for compiled bundle distributions, creator documentation, the deferred registry index when publication requires it, and an external clean-room publish/install acceptance test.
- **Independently verified facts:** The current repository has package consumption and validation under `internal/packages` and `smidja pkg`, but no package-authoring guide or template implementation. The plan records `digitalygo/smidja-packages` as deferred; its current external GitHub state was not rechecked in this checkpoint.
- **Decision and impact:** Fase 5 remains pending and the plan status remains `in-progress`. Template and registry-index work may affect additional repositories, so implementation cannot begin without explicit multi-repository approval.
- **Next action:** Present the remaining Fase 5 work, repository boundaries, acceptance test and gate sequence to the user.

#### Checkpoint 2026-09-05T00:38:10+02:00: Fase 5 scope approved and execution resumed

- **Event:** The user commissioned Fase 5 and explicitly approved the bounded multi-repository implementation.
- **Planner prediction:** The baseline requires a package-template repository, creator documentation and autonomous publication by an external creator. V-017 removes every no-rebuild extension mechanism.
- **Subagent claims:** Locator and analyzer agents found no applicable DRC or EXP files, no existing creator guide, no template repository and two distinct distribution contracts. The solution architect recommended a compiled-bundle template plus separate documentation for compiled bundles and content-only packages.
- **Orchestrator finding:** `smidja pkg` installs content-only packages directly from public GitHub repositories and does not consume a registry index. The complex missing creator surface is the compiled bundle template. `digitalygo/smidja-digitalygo` is sufficient as a read-only reference and does not require modification.
- **Independently verified facts:** `digitalygo/smidja` is clean and synchronized on default branch `alpha` at a8a32dc. `digitalygo/smidja-digitalygo` is clean and synchronized on default branch `main` at 612f3ef. GitHub has no `digitalygo/smidja-bundle-template` or `digitalygo/smidja-packages`. The user approved modifying `digitalygo/smidja` and creating the public `digitalygo/smidja-bundle-template`, while excluding the other repositories.
- **Decision and impact:** Fase 5 implementation starts across the two approved repositories. `digitalygo/smidja-packages` remains deferred because it has no runtime consumer or current discovery requirement. The original external-creator criterion remains a closure gate, and an internal rehearsal will not be mislabeled as external evidence.
- **Next action:** Create and implement the public compiled-bundle template, write both creator guides in the harness repository, then run repository-specific verification and gates.

#### Checkpoint 2026-09-05T02:11:13+02:00: Fase 5 technical delivery completed and gated

- **Event:** The approved two-repository Fase 5 implementation, publication, technical gates and internal clean-room rehearsal completed. The external-human completion criterion remains open.
- **Planner prediction:** Fase 5 expected a package-template repository, creator documentation and an external creator able to publish autonomously.
- **Subagent claims:** The backend implementation supplied a compiled-bundle template with immutable harness pin, embedded content, one offline extension, customization tooling, tests, cross-build and release workflows. The documentation implementation supplied separate creator guides for compiled bundles and content-only packages. Quality and security reviewers returned PASS.
- **Orchestrator finding:** The first remote clean-room rehearsal exposed rewrite-sensitive hardcoded test fixtures after customization. A focused test-only correction made identity fixtures derive the current module, exercised the dirty-tree guard in a real git repository and validated focused identity tests inside a customized copy. A second rehearsal from the final remote commit passed for the exact previously failing origin.
- **Independently verified facts:** `digitalygo/smidja-bundle-template` is public, uses default branch `main`, is enabled as a GitHub template and is synchronized at 83492b2. Remote CI run 33931839192 passed on that commit. `go mod verify`, formatting, vet, full tests, race tests, shellcheck, actionlint, workflow parsing, four Linux/macOS amd64/arm64 static builds, sorted checksum verification and release identity smoke passed; aggregate statement coverage is 95.1%. The frozen product artifact 18f45fb089f62567ba6ea3b5a2a704f1c54cad5951662cf3919b6a1f6b2d9c8e received quality PASS and focused security PASS. The follow-up test-only patch 691c3f10781143486723f011d8a3530053e4e059eb99017619bbad05678f377c received quality PASS; a new security review was not required because production and workflow content did not change. A fresh clone was customized to `github.com/acme/cool-bundle`, committed locally, passed full and race tests, produced all release assets with valid checksums, and reported the derived origin, version and commit correctly. `digitalygo/smidja` published README and decision-record alignment plus `docs/creating-bundles.md` and `docs/creating-content-packages.md` at 42cb024; Markdown structure, links, whitespace and sample manifest hashes and sizes passed human verification. Documentation adds no executable lines, so tests and coverage are N/A for that slice.
- **Decision and impact:** Planned and actual harness files are `README.md`, `docs/decision-record-draft.md`, `docs/creating-bundles.md` and `docs/creating-content-packages.md`; the new template contains 25 source, test, content, workflow and repository files. `digitalygo/smidja-digitalygo` remained read-only and `digitalygo/smidja-packages` remained deferred. Technical implementation is complete, but Fase 5 and the overall plan remain `in-progress` because an internal agent rehearsal cannot satisfy the external-human criterion.
- **Next action:** Ask a genuinely external creator to generate a repository from the template, customize it, publish a `v*` release and verify it from a clean environment without Digitalygo intervention.

#### Checkpoint 2026-09-05T13:52:55+02:00: registry deferral reversed by user

- **Event:** The user explicitly requested creation of `digitalygo/smidja-packages`, reversing the demand-driven deferral after reviewing its maintenance trade-offs.
- **Planner prediction:** Fase 3 introduced a central package index and Fase 5 retained it as a deferred ecosystem deliverable.
- **Subagent claims:** Codebase and trace locators confirmed that the remote registry has no current CLI consumer and is separate from the compiled-bundle template. The solution architect proposed a public, static, publisher-maintained discovery registry with deterministic local validation and generated outputs.
- **Orchestrator finding:** A registry can remain low-maintenance if package repositories and GitHub tags remain authoritative, publishers submit metadata through pull requests, versions are not mirrored and required CI performs no network crawling.
- **Independently verified facts:** `digitalygo/smidja` is clean and synchronized on default branch `alpha` at c83f0ea. GitHub and the local workspace have no `digitalygo/smidja-packages`. The authenticated Digitalygo maintainer is `lucanori`. No DRC, EXP or CONTRIBUTING file governs this repository.
- **Decision and impact:** Create public `digitalygo/smidja-packages` now and update only the registry references in `digitalygo/smidja`. Do not add registry-aware installation or `smidja pkg search`; direct installation remains independent. This supersedes only the registry-deferral portion of V-018.
- **Next action:** Implement the empty but useful registry, validate and publish it, then record gates and return to external creator acceptance.

#### Checkpoint 2026-09-05T15:26:32+02:00: public package registry completed and gated

- **Event:** `digitalygo/smidja-packages` was implemented, published and protected as the Fase 5 content-package discovery registry.
- **Planner prediction:** The ecosystem required a central package index without changing the content-only package runtime contract.
- **Subagent claims:** Backend implementation supplied strict descriptor parsing, filesystem validation, deterministic JSON and Markdown generation, self-service governance, read-only CI and adversarial tests. Documentation updates linked the registry while preserving direct installation. Quality and security reviewers returned PASS.
- **Orchestrator finding:** The registry starts empty but is immediately useful as a governed discovery and contribution surface. Package publishers own one stable descriptor and never synchronize release versions, so ongoing work is limited to moderated listing changes rather than central package maintenance.
- **Independently verified facts:** Public `digitalygo/smidja-packages` is synchronized on default branch `main` at 5c8ec5f. Remote CI run 33968792935 passed. Organization ruleset 13725978 requires pull requests, one approving CODEOWNERS review, stale-review dismissal and blocks deletion and non-fast-forward updates; repository protection additionally requires the current `verify` status, resolved conversations and blocks force-push and branch deletion. Formatting, `go mod tidy`, build, vet, shuffled full tests, race tests, Linux, macOS and Windows static builds, repeated generation, exact output checks, actionlint, YAML and Markdown validation all passed. Aggregate statement coverage is 95.1%. Frozen artifact e81069e16bccea1646a51522fc4b478c7c27144df629fdbd690ebeecbd5b25bb received quality PASS and focused security PASS. The registry is standard-library only, contains no package entries and publishes a valid empty `index.json` and `catalog.md`. Harness documentation at 94e4915 passed human structure, link and wording verification; tests and coverage are N/A for that documentation-only slice.
- **Decision and impact:** The V-019 registry follow-up is complete. Actual registry scope is 25 files covering tooling, tests, generated outputs, CI, governance and documentation; harness changes are limited to `README.md`, `docs/creating-content-packages.md` and `docs/decision-record-draft.md`. No harness Go behavior, bundle template or reference bundle changed. The phase returns to the external creator acceptance gate.
- **Next action:** Obtain genuine external publication evidence or explicit approval to defer the adoption criterion, then close Fase 5 and the overall plan.

## Plan-variation ledger

### Variation V-001: benchmark series limited to one paired trial per task

- **Baseline reference:** Fase 0 predicted verification: confronto diretto con Pi sugli stessi task; architect proposal specified at least three paired trials per harness and task with alternating execution order.
- **Discovered evidence:** Orchestrator finding: each paired trial costs a real model round trip; single-trial results already demonstrate the completion criterion (working demo plus comparison numbers).
- **Decision:** Accepted for Fase 0 closure. Results labeled indicative, not a settled ranking; repeated trials deferred.
- **Scope and downstream impact:** docs/benchmarks/phase-0.md limitations section records the deviation. No impact on later phases.
- **Approval:** Within Fase 0 scope (spike evidence, not product behavior); no approval gate required.
- **Resolution:** Checkpoint 2026-08-25T03:00:00+02:00.

### Variation V-002: idle RSS measured manually instead of via bench/metrics.sh

- **Baseline reference:** Architect proposal: idle RSS via metrics.sh with FIFO-held stdin, 20 samples.
- **Discovered evidence:** Orchestrator finding: metrics.sh reuses startup arguments (e.g. --version) in the idle phase, so the process exits before sampling (0 live samples for both harnesses).
- **Decision:** Accepted: manual measurement with the same procedure and command templates produced the recorded numbers; script fix deferred as pending bench tooling work.
- **Scope and downstream impact:** Documented in docs/benchmarks/phase-0.md; metrics.sh fix is open tooling debt.
- **Approval:** Measurement-procedure substitution only; no approval gate required.
- **Resolution:** Checkpoint 2026-08-25T03:00:00+02:00.

### Variation V-003: bench result detail reduced to generic reason strings

- **Baseline reference:** Architect proposal: run-task records success, wall time, tool calls, turns, tokens, and final diff per trial.
- **Discovered evidence:** Security review rounds R4-R7 demonstrated that any captured diagnostic channel from model-influenced executions is either forgeable or a hang vector under the same-UID threat model.
- **Decision:** Accepted: result.txt/results.tsv carry verdict plus generic reason string only; full transcript remains in out.log; rich per-check values dropped from automated recording.
- **Scope and downstream impact:** Bench output format only; no impact on harness runtime behavior or later phases.
- **Approval:** Security-mandated hardening of tooling output; no requirement-level behavior change; no approval gate required.
- **Resolution:** Checkpoint 2026-08-25T05:30:00+02:00.

### Variation V-007: per-stream tool-call count cap removed, loop detector planned in core

- **Baseline reference:** Security gate rounds R1-R2 introduced MaxToolCalls=32 per SSE stream as a protocol sanity bound; checkpoint CP-012 kept it deliberately.
- **Discovered evidence:** User decision 2026-08-25: remove the cap; a core loop detector (new Fase 1 core component) will interrupt pathological behavior.
- **Decision:** Accepted: MaxToolCalls cap and sentinel removed from internal/openrouter. Memory stays bounded by retained caps (MaxStreamBytes 64 MiB cumulative, MaxToolArgsBytes 1 MiB per call); behavioral interruption becomes the loop detector's responsibility (to be implemented as core in Fase 1).
- **Scope and downstream impact:** internal/openrouter only; Fase 1 scope gains the loop detector as a core component alongside smart context management.
- **Approval:** Explicit user decision.
- **Resolution:** Implemented and verified 2026-08-25 (all tests green, grep evidence of full removal).

### Variation V-009: all memory/truncation limits aligned to Pi

- **Baseline reference:** Security gate rounds R1-R7 introduced protocol-level stream caps (64 MiB total via countingReader, 4 MiB text, 4 MiB thinking, 1 MiB args/call, 100k events); spike shipped tool caps at 2000 lines / 1 MiB with head+tail exec capture and 1 MiB config default.
- **Discovered evidence:** User decision 2026-08-25: "i limiti di memoria mettiamoli uguali a pi, tutti e 6". Verified Pi values from source: DEFAULT_MAX_LINES=2000, DEFAULT_MAX_BYTES=50KB (truncate.js), truncateTail semantics with full output saved to temp file; NO protocol-level stream caps exist in Pi.
- **Decision:** Accepted: (1) read/exec truncation aligned to 2000 lines / 50 KB whichever-first with Pi-style markers and full output in temp files; (2) ALL protocol-level caps removed from internal/openrouter (stream bytes, text, thinking, args/call, events) including the security-review hardening; (3) config exec default lowered 1 MiB -> 50 KB. Consequence accepted: a hostile/malfunctioning endpoint can now grow memory without bound until the OS intervenes, as in Pi.
- **Scope and downstream impact:** internal/openrouter (caps + limit tests removed), internal/tools (truncation mechanism rewritten Pi-style, limits configurable via Deps), internal/config (exec default). Security posture regression vs spike hardening is explicit and user-approved; future security gates must treat it as accepted design, not finding.
- **Approval:** Explicit user decision.
- **Resolution:** Implemented and verified 2026-08-25 (checkpoint 2026-08-25T12:00:00+02:00). Open flag: write/edit 2 MiB input cap retained pending user direction (not part of the six rows).

### Variation V-010: write/edit input size cap removed for Pi parity

- **Baseline reference:** Spike shipped a 2 MiB input cap on write content / edit inputs (architect proposal hardening); flagged open in V-009.
- **Discovered evidence:** Verified from installed Pi 0.84.2 source: write.js and edit.js contain no input size limits. User instruction: check Pi and align.
- **Decision:** Accepted: writeMaxBytes constant and the oversize rejection removed; write accepts arbitrary content, matching Pi.
- **Scope and downstream impact:** internal/tools only. A model can now write arbitrarily large files in one call, as in Pi.
- **Approval:** Explicit user instruction to match Pi.
- **Resolution:** Implemented and verified 2026-08-25 (checkpoint 2026-08-25T12:15:00+02:00).

### Variation V-015: Fase 4 revised scope frozen (content parity, cache-preserving reopen policy, gateways)

- **Baseline reference:** Fase 4 baseline (gateway Telegram/Discord/sessioni remote); solution-architect proposal 2026-08-26.
- **Discovered evidence:** User decisions: Discord excluded; web gateway included v0; content loading parity with Pi under ~/.smidja (skills, agents-subagents, sessions incl subagent-sessions layout, AGENTS.md, auth.json, models-store.json, models.json, settings.json) with dual origin bundle-first > .smidja dynamic; models consumed temporarily from Pi unified endpoint (pi.dev/api/models, verified live) until Smidja owns one - endpoint URL stays configurable; every Telegram message drives a headless-like run that stops at completion while REOPENING the same session JSONL to preserve provider-side prompt cache across invocations (applies uniformly to telegram/headless/interactive); resume shows the user a deterministic digest (last intents, turn/tool counts, last excerpt) NOT injected into model context; Telegram Bot API 10.x rich messages are MANDATORY for AI output (user verified: sendRichMessage/sendRichMessageDraft, 32768 UTF-8 chars, native tables/math/thinking blocks) replacing chunking as primary path with legacy fallback. CORRECTION by user overriding architect+orchestrator amendment: CacheMissAfter staleness REMAINS a legitimate prune/compact gate - rationale: cached input ~1/10 cost, expired cache makes mutations free-to-beneficial on the very first full-price call; compact is verbatim selection (never a summary) paying one expensive turn then re-caching cheaper; safety threshold acts regardless of cache state; warm cache freezes the prefix.
- **Decision:** Accepted: four-wave implementation per architect structure (contracts/fixtures -> content+models | sessions/cache | gateway kernel -> telegram rick-message transport + web + bundle adaptation -> integration/gates). Confirmed architectural findings to fix in-scope: Store lacks append-reopen; Loader silently skips malformed lines (strict mode required); skills catalog missing ~/.smidja/skills source; authstore needs cross-process RMW locking.
- **Scope and downstream impact:** Provider request-prefix fidelity becomes a tested contract (replay goldens per driver); gateway runs N concurrent sessions with per-workspace exclusive locks; journal durability under ~/.smidja/gateway/.
- **Approval:** Explicit user decisions; live Telegram testing will use a user-provided bot token stored locally in authstore (never in fixtures/chat).
- **Resolution:** Implementation starts.

### Variation V-013: Fase 3 scope frozen (real Digitalygo bundle, MCP client in core, packaging stack)

- **Baseline reference:** Fase 3 steps 1-4; open MCP question from Fase 3 discussion.
- **Discovered evidence:** User decisions 2026-08-26: current-time becomes core; openrouter-attribution ignored; context-hygiene already absorbed by core verbatim selector and compaction hooks; loop-detector already core; no-rebuild extensions ruled out permanently (built-in is the harness identity); smidja does NOT expose an MCP server but MUST support consuming external MCP servers via config, like Pi and OpenCode.
- **Decision:** Accepted: Fase 3 = (a) real Digitalygo bundle content (10 role skills, 32 agent definitions, settings/models defaults from dotfiles); (b) Go port of extensions calculator, unit-converter, youtrack, exa, mistral-ocr-pdf-to-md, replicate-generation; (c) native MCP client in core (stdio transport, JSON-RPC 2.0, tools discovery/call) driven by config, enabling chrome-devtools-mcp and figma-mcp usage; (d) package format + validation; (e) index repo digitalygo/smidja-packages; (f) CLI smidja pkg list/install/remove/update. Solution-architect validation required before implementation on registry/MCP design.
- **Scope and downstream impact:** Two user extensions dropped from porting list as superseded (current-time core, attribution skipped); chrome-devtools-mcp and figma-mcp preserved through the MCP client instead of direct ports.
- **Approval:** Explicit user decisions; architect gate pending on registry and MCP internals.
- **Resolution:** Pending implementation.

### Variation V-011: Digitalygo bundle packaging deferred until base harness is complete

- **Baseline reference:** Fase 1 step 5: "Packaging della repo Digitalygo con i nostri contenuti baked-in".
- **Discovered evidence:** Packaging requires a second repository (Digitalygo package repo) whose location/creation the user explicitly deferred: "la creiamo dopo prima finiamo l'harness di base".
- **Decision:** Accepted: Fase 1 closes with packaging pending as a tracked follow-up; sdk.Bundle + smidja.go entry seam already provide the composition surface.
- **Scope and downstream impact:** Bundle wiring wave (3C) not executed; multi-repo approval will be requested when the work is scheduled.
- **Approval:** Explicit user decision, recorded in checkpoint 2026-08-25T17:30:00+02:00.
- **Resolution:** Superseded by V-012: repo created by user at github.com/digitalygo/smidja-digitalygo (cloned locally); packaging scheduled inside Fase 2.

### Variation V-014: Fase 3 design validated and frozen (MCP client, tool ownership, packages)

- **Baseline reference:** Fase 3 steps per V-013; solution-architect challenge validation required by user.
- **Discovered evidence:** Architect proposal accepted with orchestrator decisions: rich MCP content (images/audio) fails explicitly instead of flattening; sdk.API gains ConfigValue backed by the resolved config chain; workspace-defined MCP commands require --allow-workspace-mcp (fail-closed).
- **Decision:** Accepted. Key elements: internal/mcp generic bridge (NDJSON-first framing with Content-Length fallback respawn-one-time), initialize handshake with pinned protocol version, no in-flight tools/call replay after restart (outcome-unknown error), restart budget 3/60s exponential backoff; tools split core (calculator/unit-converter/current-time -> internal/tools/builtin) vs bundle (youtrack/exa/mistralocr/replicategeneration as sdk.ToolHook extensions with lazy credentials); skills via /skill command injecting user-role message with provenance (agents/*.md embedded but deferred until subagent runtime); package format v0 content-only with manifest schema, dual integrity (commit pin + per-file sha256), staged atomic install under ~/.smidja/packages/<id>/<version>/, flat dependency resolution with greatest-minimum rule, verifier seam recording authenticity=unverified; smidja pkg CLI (install/list/inspect/activate/deactivate/update/verify/uninstall). No Go plugins from packages ever.
- **Scope and downstream impact:** Wave 0 config seams; wave 1 five parallel lanes; wave 2 two integration owners (harness cli/pkg/mcp runtime, bundle composition); wave 3 compatibility smoke against pinned chrome-devtools-mcp/figma-mcp npm versions plus full gates.
- **Approval:** User mandated architect validation before execution; executed on this basis.
- **Resolution:** Implementation starts.

### Variation V-012: Fase 2 provider scope revised (all-Pi-providers parity, exclusions, architect corrections)

- **Baseline reference:** Fase 2 step 1 listed a fixed provider subset (Anthropic, OpenAI, DeepSeek, Alibaba Qwen, Moonshot Kimi, Z.ai).
- **Discovered evidence:** User decision 2026-08-26: implement ALL providers Pi supports, both API-key and OAuth, porting from pi-ai TS sources; flag anything not replicable. Exclusions confirmed by user: Amazon Bedrock (SigV4 project not wanted now) and GitHub Copilot. Radius excluded (requires the pi-messages protocol, out of scope). Kimi Coding device-code OAuth added. solution-architect challenge validation returned ISSUES, all accepted: protocol drivers before provider config variants; internal/openrouter kept as compatibility facade over the new openai-completions driver; auth.json full Pi shape ({type:"api_key"|"oauth", access/refresh/expires,...} unknown fields preserved, 0700/0600, atomic RMW); exact OAuth callback specs per provider (Codex localhost:1455/auth/callback, Anthropic localhost:53692/callback, Kimi/xAI device-code, OpenRouter ephemeral loopback); canonical provider identity (providerId, modelId, api dialect); sdk.Run composition seam required before bundle; origin format standardized github.com/owner/repo; macOS update path = brew only (internal/update remains linux-only), Cellar installs must not self-update; bundle CI must publish release assets + checksums.
- **Decision:** Accepted: Fase 2 executed as 8 blocks: (1) release pipeline; (2) sdk.Run seam; (3) bundle repo; (4) provider core refactor + auth layer; (5) wire protocols anthropic-messages/gemini/openai-responses(+azure,codex adapters); (6) API-key provider manifest rollout; (7) OAuth flows openrouter/anthropic/codex/xai/kimi; (8) CLI auth UX + brew tap.
- **Scope and downstream impact:** Provider surface becomes full Pi parity minus bedrock/copilot/radius; Fase 2 completion criterion unchanged (external installs and works); manual multi-machine tests deferred post-development (user decision).
- **Approval:** Explicit user approval of scope and exclusions; architect corrections accepted by orchestrator.
- **Resolution:** Execution starts at block 1.

- **Baseline reference:** Fase 1 step 5: "Packaging della repo Digitalygo con i nostri contenuti baked-in".
- **Discovered evidence:** Packaging requires a second repository (Digitalygo package repo) whose location/creation the user explicitly deferred: "la creiamo dopo prima finiamo l'harness di base".
- **Decision:** Accepted: Fase 1 closes with packaging pending as a tracked follow-up; sdk.Bundle + smidja.go entry seam already provide the composition surface.
- **Scope and downstream impact:** Bundle wiring wave (3C) not executed; multi-repo approval will be requested when the work is scheduled.
- **Approval:** Explicit user decision, recorded in checkpoint 2026-08-25T17:30:00+02:00.
- **Resolution:** Pending; scheduled after base-harness completion.

- **Baseline reference:** Spike shipped a 2 MiB input cap on write content / edit inputs (architect proposal hardening); flagged open in V-009.
- **Discovered evidence:** Verified from installed Pi 0.84.2 source: write.js and edit.js contain no input size limits. User instruction: check Pi and align.
- **Decision:** Accepted: writeMaxBytes constant and the oversize rejection removed; write accepts arbitrary content, matching Pi.
- **Scope and downstream impact:** internal/tools only. A model can now write arbitrarily large files in one call, as in Pi.
- **Approval:** Explicit user instruction to match Pi.
- **Resolution:** Implemented and verified 2026-08-25 (checkpoint 2026-08-25T12:15:00+02:00).

### Variation V-008: full-parity extension context API from v0

- **Baseline reference:** Architect proposal suggested a minimal handler context (tool/command registration + read access) for the spike-to-MVP path.
- **Discovered evidence:** User decision 2026-08-25: the handler context must match Pi's capability surface completely ("deve essere uguale a pi, non ha senso fare mezze implementazioni").
- **Decision:** Accepted: smidja v0 exposes the full action surface in the hook context - tool and command registration, session entry appending, UI callbacks, model registry access - capability-for-capability with Pi.
- **Scope and downstream impact:** Larger Fase 1 design/implementation surface for internal/extensions (or equivalent core package); avoids rework at gateway (Fase 4) and ecosystem (Fase 5) phases.
- **Approval:** Explicit user decision, recorded in checkpoint 2026-08-25T11:45:00+02:00.
- **Resolution:** To be implemented in Fase 1.

### Variation V-004: smart context management placed in smidja core

- **Baseline reference:** Planner baseline, architecture component 5: "Gestione contesto smart, baked-in nel pacchetto Digitalygo, parametri configurabili".
- **Discovered evidence:** User decision 2026-08-25: context management is part of the base project, not a package-baked extension; packages configure parameters/content.
- **Decision:** Accepted: implement the context management mechanism as core packages of the base harness; Digitalygo package content only sets defaults/parameters.
- **Scope and downstream impact:** Fase 1 implementation location moves from package layer to internal/core packages; no behavioral change to the planned feature set (double-criterion prune/compact, safety compact, pin, verbatim selection).
- **Approval:** Explicit user decision, recorded in checkpoint 2026-08-25T10:30:00+02:00.
- **Resolution:** To be implemented in Fase 1.

### Variation V-005: retry default maxRetries = 10 instead of Pi's 3

- **Baseline reference:** Fase 1 step 1 and user decision: retry policy copied identically from Pi.
- **Discovered evidence:** Pi default is maxRetries 3 with exponential agent-level backoff; user requested identical policy but default 10 retries.
- **Decision:** Accepted: classification patterns, backoff formula, events and configurability copied from Pi; default maxRetries = 10.
- **Scope and downstream impact:** internal/config gains retry settings (SMIDJA_RETRY_MAX_RETRIES etc.); internal/agent loop gains retry wrapper with auto-retry events.
- **Approval:** Explicit user decision, recorded in checkpoint 2026-08-25T10:30:00+02:00.
- **Resolution:** To be implemented in Fase 1.

### Variation V-006: agentic loop runs unbounded like Pi, runaway guards removed

- **Baseline reference:** Checkpoint 2026-08-25T10:30:00+02:00 decision 5 noted maxRounds/maxToolCalls guards as deliberate divergence from Pi; spike implementation shipped MaxRounds=20 / MaxToolCalls=64 defaults in internal/config enforced by internal/agent/loop.go.
- **Discovered evidence:** User decision 2026-08-25 after reviewing the divergence explanation: keep the loop without limits exactly like Pi (`while (true)` in pi-agent-core agent-loop.js); runaway protection comes from the user interrupting, as in Pi.
- **Decision:** Accepted: remove MaxRounds/MaxToolCalls config fields, env overrides, constants and loop enforcement; loop terminates on final answer, error, or context cancellation only.
- **Scope and downstream impact:** internal/config (fields, env vars SMIDJA_MAX_ROUNDS/SMIDJA_MAX_TOOL_CALLS removed), internal/agent/loop.go (budget enforcement and ErrMaxRounds/call-limit errors removed), related tests updated. Behavior change vs spike: an unbounded tool loop now burns tokens until interrupted.
- **Approval:** Explicit user decision.
- **Resolution:** Implemented in checkpoint 2026-08-25T11:00:00+02:00.

- **Baseline reference:** Architect proposal: run-task records success, wall time, tool calls, turns, tokens, and final diff per trial.
- **Discovered evidence:** Security review rounds R4-R7 demonstrated that any captured diagnostic channel from model-influenced executions is either forgeable or a hang vector under the same-UID threat model.
- **Decision:** Accepted: result.txt/results.tsv carry verdict plus generic reason string only; full transcript remains in out.log; rich per-check values dropped from automated recording.
- **Scope and downstream impact:** Bench output format only; no impact on harness runtime behavior or later phases.
- **Approval:** Security-mandated hardening of tooling output; no requirement-level behavior change; no approval gate required.
- **Resolution:** Checkpoint 2026-08-25T05:30:00+02:00.

### Variation V-016: Fase 4 phone-workday criterion deferred after technical closure

- **Baseline reference:** Fase 4 completion criterion required one complete workday from a phone without opening a laptop.
- **Discovered evidence:** All implementation and technical gates passed, but privacy-preserving local journal metadata showed only two failed Telegram turns on one date and no completed turn, so the real-world criterion could not be verified.
- **Decision:** Defer the phone-workday criterion as post-phase adoption evidence; do not treat it as an implementation or gate blocker.
- **Scope and downstream impact:** No code or gateway behavior changes. Fase 4 may close technically while the deferred adoption check remains visible for later real use.
- **Approval:** Explicit user approval on 2026-09-04.
- **Resolution:** Fase 4 completion checkpoint 2026-09-04T22:30:32+02:00.

### Variation V-017: Fase 5 no-rebuild extension step removed

- **Baseline reference:** Fase 5 step 3 proposed subprocess or WASM extensions without rebuilding the binary if demand emerged.
- **Discovered evidence:** V-013 records the user's permanent decision that built-in extension composition is the harness identity and no no-rebuild extension mechanism will be supported.
- **Decision:** Remove subprocess, WASM, Go plugins and equivalent dynamic mechanisms from Fase 5. Preserve compiled Go extensions through bundle builds.
- **Scope and downstream impact:** Fase 5 is limited to the bundle/package template, creator documentation, deferred registry index and external publication acceptance test.
- **Approval:** Existing explicit user decision recorded by V-013 on 2026-08-26.
- **Resolution:** Fase 5 handoff checkpoint 2026-09-04T22:30:33+02:00.

### Variation V-018: Fase 5 separates the compiled template from content-package guidance

- **Baseline reference:** Fase 5 step 1 predicted one package-template repository, while later package work introduced a separate content-only runtime package format and a deferred registry index.
- **Discovered evidence:** Codebase analysis verified that compiled bundles and content-only packages have incompatible repository layouts. `smidja pkg` installs directly from public GitHub repositories and has no registry-index consumer. The complex missing creator surface was the compiled bundle build and release chain.
- **Decision:** Create the public `digitalygo/smidja-bundle-template` for compiled distributions, document content-only package authoring separately in `digitalygo/smidja`, and keep `digitalygo/smidja-packages` deferred until discovery demand exists.
- **Scope and downstream impact:** Fase 5 changed only `digitalygo/smidja` and the new template repository. `digitalygo/smidja-digitalygo` stayed read-only, no content-package template was added and no runtime package contract changed.
- **Approval:** The user explicitly approved this two-repository boundary on 2026-09-05.
- **Resolution:** Fase 5 technical delivery checkpoint 2026-09-05T02:11:13+02:00.

### Variation V-019: public package registry created before organic demand

- **Baseline reference:** V-018 deferred `digitalygo/smidja-packages` until package discovery demand emerged.
- **Discovered evidence:** The user preferred creating the registry now after reviewing the future maintenance trade-offs. A static publisher-maintained directory avoids release synchronization and central package hosting.
- **Decision:** Create the public registry immediately with self-service descriptor pull requests, deterministic generated index and catalog, and offline validation. Keep installation and version resolution on package repositories.
- **Scope and downstream impact:** Adds new repository `digitalygo/smidja-packages` and bounded registry-link updates in `digitalygo/smidja`. No harness Go code, package format, bundle template or reference bundle changes.
- **Approval:** Explicit user request on 2026-09-05.
- **Resolution:** Registry implementation starts at checkpoint 2026-09-05T13:52:55+02:00.

## Closure evidence

### Final outcome

- **Fase 0, spike: COMPLETED** on 2026-08-25 with independently verified CLI, benchmark and Pi-v3 session evidence.
- **Fase 1, MVP interno: COMPLETED** on 2026-08-25 with code and gates complete; daily team adoption and multi-machine update evidence remain deferred operational checks.
- **Fase 2, distribuzione: COMPLETED** on 2026-08-26 with public repositories, release v0.1.0 and brew tap verified; unassisted external installation remains deferred adoption evidence.
- **Fase 3, pacchetti opzionali: COMPLETED** on 2026-08-26 with package format, MCP client, CLI lifecycle and tests gated; team usage and registry-index creation remain demand-driven follow-ups.
- **Fase 4, gateway remoto: COMPLETED** on 2026-09-04 with Telegram and web gateways, session continuity, content/configuration parity, quality PASS and security PASS. The user approved deferring the phone-workday criterion under V-016.
- **Fase 5, ecosistema: PENDING EXTERNAL ACCEPTANCE.** Template, creator guides and public package registry implementation, publication and gates completed on 2026-09-05; the baseline external-creator criterion remains open, so the overall plan stays `in-progress`.

### Quality and security evidence

- Fase 0 quality and security gates remain recorded in its execution checkpoints and operation record, including final quality artifact 8161e0bfdccc9a66d0b806001093327b9cfee65c41f0a694f4b9804c4f25a6b4 and security artifact 064cd7a51e8f44d93baac56ef8d7517cce147b27c90693735da8286d501843fb.
- Fase 1 quality and security PASS are recorded at checkpoints 2026-08-25T15:30:00+02:00 and 2026-08-25T16:30:00+02:00.
- Fase 2 quality and security PASS are recorded at checkpoint 2026-08-26T01:30:00+02:00; release and brew verification followed at 2026-08-26T12:00:00+02:00.
- Fase 3 quality and security PASS are recorded at checkpoint 2026-08-26T20:00:00+02:00 on artifact 672c8ace.
- Fase 4 deterministic, dedicated quality and focused security gates all passed on target 7184165fe3599c343813e954a289d4b3080dc74a, tree 1b329e71f65fad3118eed4f2e413ecb20127432d, manifest 2c9d6c70469f28e928c002fd0a73dfdc9899edefa852caedff11ee1e738176f3, and binary diff 05b81207373af1aea4e0c7d2a610bf2c2af6b5cafd69f3d7b6c0ccfbcd8d57b1. Aggregate delta-package coverage was 89.2%.
- Fase 4 verification included supported static cross-builds, full build/vet/test, repeated auth concurrency and provider replay tests, 100 gateway cancellation repetitions and race tests across all 15 delta packages. The unsupported full Windows build and pre-existing MCP stress flake remain out-of-delta signals; focused Windows builds for changed lock packages passed.
- Fase 5 template deterministic and quality gates passed on frozen artifact 18f45fb089f62567ba6ea3b5a2a704f1c54cad5951662cf3919b6a1f6b2d9c8e, including 95.1% statement coverage, race tests, shell and workflow validation, four static cross-builds, checksums and release identity smoke. Focused supply-chain security review passed on the same artifact.
- Fase 5 clean-room regression patch 691c3f10781143486723f011d8a3530053e4e059eb99017619bbad05678f377c received quality PASS after reproducing and correcting customized-repository test failures. Security review was N/A because this follow-up changed tests only. Remote template CI run 33931839192 passed on final commit 83492b2.
- Fase 5 creator documentation at `smidja` commit 42cb024 passed human Markdown, link and manifest-example verification. Tests and coverage were N/A because the slice changed documentation only.
- Fase 5 registry artifact e81069e16bccea1646a51522fc4b478c7c27144df629fdbd690ebeecbd5b25bb received deterministic, quality and focused security PASS with 95.1% statement coverage. Remote CI run 33968792935 passed on registry commit 5c8ec5f; organization and repository branch protections were independently verified. Registry-link documentation at `smidja` commit 94e4915 passed human review with tests and coverage N/A.

### Operation record

- [Fase 4 gateway implementation operation](../operations/2026-09-04-smidja-fase4-gateway-implementation.md) summarizes the completed gateway work and links back to this evidence ledger.
- [Public package registry operation](../operations/2026-09-05-smidja-packages-registry.md) summarizes the completed registry delivery and links back to this evidence ledger.
