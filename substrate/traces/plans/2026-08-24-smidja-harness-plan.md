---
document_type: mycelium-plan
plan_id: 2026-08-24-smidja-harness-plan
status: ready-for-execution
created_at: 2026-08-24
planner: hermes-decision-draft
baseline_version: 1
execution_owner: orchestrator
execution_started_at: null
last_updated_at: 2026-08-24
---

# Piano Smidja harness

## Current execution snapshot

- **Status:** Ready for execution.
- **Baseline identity:** `2026-08-24-smidja-harness-plan`, baseline version 1, planner handoff date 2026-08-24.
- **Execution baseline:** Not started.
- **Active phase:** Fase 0, spike.
- **Last verified checkpoint:** None.
- **Last successful checks:** None.
- **Open blockers:** None.
- **Required approvals and gates:** Planner prediction: confermare la lista hook per gruppo ed eventuali eventi extra; chiudere in Fase 0 l'alternativa workspace `<repo>/.smidja/` o `.agent/`; verificare in Fase 0 quanto il formato JSONL di Pi sia stabile da allinearsi; Gate Windows: primo cliente reale su Windows; rivalutare il manifest con `includes` se l'ecosistema lo chiede; valutare il certificato Apple Developer ID quando c'è distribuzione vera; valutare estensioni senza rebuild solo se emerge il caso d'uso reale o c'è domanda.
- **Next action:** Orchestrator validates this plan and records the execution baseline.

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

### Fase 1, MVP interno execution checkpoints

### Fase 2, distribuzione execution checkpoints

### Fase 3, pacchetti opzionali execution checkpoints

### Fase 4, gateway remoto execution checkpoints

### Fase 5, ecosistema execution checkpoints

## Plan-variation ledger

## Closure evidence

### Final outcome

### Quality and security evidence

### Operation record
