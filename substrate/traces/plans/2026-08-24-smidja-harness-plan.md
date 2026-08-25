---
document_type: mycelium-plan
plan_id: 2026-08-24-smidja-harness-plan
status: in-progress
created_at: 2026-08-24
planner: hermes-decision-draft
baseline_version: 1
execution_owner: orchestrator
execution_started_at: 2026-08-25T00:05:00+02:00
last_updated_at: 2026-08-25T00:05:00+02:00
---

# Piano Smidja harness

## Current execution snapshot

- **Status:** In progress. Fase 0 completed with verified evidence; Fase 1 design decisions being collected.
- **Baseline identity:** `2026-08-24-smidja-harness-plan`, baseline version 1, planner handoff date 2026-08-24.
- **Execution baseline:** Started 2026-08-25T00:05:00+02:00, repository baseline commit b5cd629.
- **Active phase:** Fase 1, MVP interno (design decisions in progress; implementation not started).
- **Last verified checkpoint:** CP-012 runaway guards removed (loop unbounded like Pi); prior CP-011 Fase 1 design decisions from user.
- **Last successful checks:** go build/vet/gofmt clean, go test -count=1 ./... all green, CGO_ENABLED=0 static binary verified, live OpenRouter demo successful (also via .env credentials), paired Pi benchmarks recorded, quality gate PASS, cumulative security gate PASS.
- **Open blockers:** None for Fase 0. Fase 1 implementation pending interface design answers from the user.
- **Required approvals and gates:** unchanged from baseline.
- **Next action:** Collect user decisions on extension interface shape (single large interface vs per-group interfaces) and v0 hook set, then delegate Fase 1 implementation.

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

#### Checkpoint 2026-08-25T12:15:00+02:00: write/edit input cap removed, Fase 1 design decisions complete

- **Event:** Variation V-010 implemented and verified; the last open design flag is closed.
- **Planner prediction:** Not in baseline.
- **Subagent claims:** go-dev removed writeMaxBytes and the oversize rejection from internal/tools; added TestWriteAcceptsLargeContent (3 MiB write succeeds); schema description updated.
- **Orchestrator finding:** Verified: go build/vet/gofmt clean, full suite green, grep confirms writeMaxBytes gone. Pi source verified to have no write/edit input limits.
- **Independently verified facts:** Full suite green post-change.
- **Decision and impact:** All memory/size limits in smidja now match Pi exactly. The complete Fase 1 decision set is collected: option B interfaces, incremental hooks, registration ordering, full-parity context API, retry identical to Pi with default 10, error policy from Pi, context management as core, loop detector as core, unbounded loop, all limits Pi-aligned.
- **Next action:** Delegate detailed Fase 1 implementation plan (hook registry + retry + error policy + full-parity ctx + core context management + loop detector) via solution-architect, then implement in waves.

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

### Fase 3, pacchetti opzionali execution checkpoints

### Fase 4, gateway remoto execution checkpoints

### Fase 5, ecosistema execution checkpoints

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

## Closure evidence

### Final outcome

- **Fase 0, spike: COMPLETED** on 2026-08-25 with independently verified evidence.
- Deliverables: minimal agentic Go harness (module github.com/digitalygo/smidja) with OpenRouter SSE streaming, tools read/write/edit/exec, Pi-v3-aligned JSONL sessions, line-oriented CLI (-p single-shot, REPL, -version); benchmark harness under bench/; methodology and results in docs/benchmarks/phase-0.md.
- Completion criterion evidence: working CLI demo on a real task (file creation + exec round-trip against live OpenRouter) plus comparison numbers vs Pi 0.84.2 on three identical tasks: static binary 6,443,170 bytes; startup 1.49 ms vs 584.5 ms median; idle RSS 5.2 MB vs 178 MB tree; task outcomes equivalent (both solved all three; wall times within provider variance).
- Workspace open question closed within Fase 0 as planned: `<repo>/.smidja/` chosen over `.agent/`.
- Pi JSONL stability question partially resolved: format v3 verified against installed Pi 0.84.2 source and live sessions; upstream history review deferred to Fase 1 import work.
- Fasi 1-5 remain pending with their baseline gates unchanged.

### Quality and security evidence

- Quality gate: dedicated quality-gate subagent, final verdict PASS. Full-session package v3 hash 8161e0bfdccc9a66d0b806001093327b9cfee65c41f0a694f4b9804c4f25a6b4; incremental cursor advanced through security-driven deltas to ddd94a16278468ef3bde74824c5cd21627b82a5bd5ef8f9605e1b111024ccd3e (bench/run-task.sh). All earlier FAILs were package-metadata completeness or hash-canonicalization issues, corrected by refreezing.
- Security gate: dedicated security-review-specialist, eight rounds, final verdict PASS on cumulative package 064cd7a51e8f44d93baac56ef8d7517cce147b27c90693735da8286d501843fb (51 files). Seven blocking findings resolved through delegated corrections (bench runner hardened to silent synchronous checker with exit-code-only verdict; SSE stream bounded cumulatively). Informational notes only in final round.
- Canonical verification at closure (orchestrator-run): go build ./... OK; go vet ./... clean; gofmt -l . empty; go test -count=1 ./... all packages ok; CGO_ENABLED=0 static ELF confirmed via file(1); secret scan no hits; bash -n bench scripts clean; live OpenRouter smoke post-changes successful.
- Limitations: staticcheck/shellcheck/govulncheck unavailable on machine; one paired trial per benchmark task (indicative numbers); metrics.sh idle phase pending fix (manual procedure substituted and documented); cmd/smidja 0% direct coverage (thin wiring).

### Operation record
