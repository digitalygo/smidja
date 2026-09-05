# Smiðja: harness agentico Digitalygo, decisioni e roadmap

Data: 2026-08-23 (rev. 3)
Stato: bozza per discussione col team
Origine: trascrizione vocale del 2026-08-23 (`~/Downloads/kdrive/vocale-17min-trascrizione.txt`), verifica online e validazione multi-agente condotta da Hermes

## Obiettivo

Smiðja significa "forgia" in antico norreno: la forgia del codice e dell'IA. Per binario e cartelle vale la forma ASCII `smidja`.

Un harness agentico proprietario, clone concettuale di Pi (oggi `earendil-works/pi`, ex badlogic/pi-mono), scritto in Go, distribuito come binario singolo senza dipendenze. Il progetto è interamente MIT.

La caratteristica distintiva non è l'harness in sé ma la distribuzione. Un "pacchetto" è una build del binario con skill, agenti, subagenti, estensioni e prompt già dentro. Chi installa il pacchetto di qualcuno ha un harness nato con quel contenuto. Aggiornare significa aggiornare il binario, non sincronizzare file sparsi.

## Perché

- Il sync attuale via chezmoi tra colleghi funziona ma non è deterministico: dipende dal fatto che l'agente esegua un comando. Un push che diventa un pacchetto che tutti aggiornano è un'altra cosa.
- Pi dimostra che il modello "harness minimo, tutto il resto estensione" è giusto. Ma è TypeScript su Node: runtime pesante, dipendenze fragili, installazione che richiede l'ecosistema npm.
- Il valore del setup Digitalygo sta nel framework di lavoro (skill, prompt, agenti, regole). Metterlo dentro la build elimina ogni frizione di installazione e porting, anche per il singolo developer con fisso e portatile.
- Le estensioni di gestione contesto che abbiamo scritto per Pi (tool prune, compact per selezione verbatim) diventano comportamento nativo configurabile invece di codice esterno da mantenere allineato.

## Decisioni

### D1. Linguaggio: Go

Scartati TypeScript/Node per requisito (zero dipendenze, binario statico) e Rust dopo confronto:

- Il carico è orchestrazione I/O (streaming API, subprocess, file), non calcolo. Il vantaggio Rust sul controllo di memoria non cambia nulla qui, il costo di apprendimento sì: nessuno nel team lo conosce.
- Cross-compilazione Go banale (`GOOS`/`GOARCH`, `CGO_ENABLED=0`), binari statici da subito su tutte le piattaforme.
- Ecosistema pronto: SDK ufficiali `openai-go` e `anthropic-sdk-go`, bot Telegram maturi (`go-telegram/bot`), TUI provata (Charmbracelet, autore del coding agent Crush in Go), runtime WASM puro-Go (wazero) disponibile se servirà.
- Precedente di settore: OpenAI ha riscritto Codex CLI da TypeScript a nativo dichiarando le nostre identiche ragioni. Vedi evidenze in fondo.

### D2. Licenza e modello di distribuzione

- Tutto MIT. Nessuna build centrale: ogni repository fa la sua build. La repo dell'harness è la base scarna stile Pi. La repo Digitalygo è un progetto che dipende dall'harness e aggiunge i nostri contenuti e workflow.
- Composizione per catena di fork: parti da una repo, forki, aggiungi, togli, buildi. Fork del fork va bene uguale. Chi mette codice dentro la propria build se la assume: è la stessa fiducia di qualsiasi software installato.
- Canali: GitHub Releases per tutti i pacchetti, Homebrew per l'harness base (e tap per chi vuole). Niente App Store, niente notarizzazione obbligatoria.
- Non obiettivo ora: manifest con `includes` verso altri pacchetti. La catena di fork copre il caso. Si rivaluta se l'ecosistema lo chiede.

### D3. Estensioni: codice Go compilato a build time

- Un'estensione è un package Go conforme all'interfaccia di estensione del core, registrato nella build della propria repo. Niente caricamento dinamico nativo (il `plugin` package di Go è limitato a tre OS e fragile), niente ABI instabili.
- Conseguenza voluta: installare l'estensione di qualcuno significa usare (o forkare) il suo pacchetto, non scaricare un file dentro un binario esistente.
- Iterazione locale senza rebuild resta possibile sul layer dichiarativo (skill, prompt, config, definizioni agente come file MD), letto dal disco con precedenza sui baked-in.
- Decisione permanente: nessun meccanismo di estensione senza rebuild. Le opzioni subprocess JSON-RPC e WASM (wazero) non restano in attesa di un caso d'uso: sono scartate. Distribuire codice significa ricompilare il bundle.

### D4. Contenuti baked-in: go:embed

Skill, agenti, subagenti, prompt e config di default finiscono nel binario con `embed.FS`. Feature nativa di Go, zero infrastruttura extra. Se serve accessorio da filesystem, estrazione read-only in cache versionata.

### D5. Piattaforme

- Sviluppo e prima stabilizzazione su Linux.
- Pipeline release con macOS subito dopo: costo marginale quasi zero (stessa toolchain, stessi artefatti). Senza Developer ID Apple l'utente fa il workaround di Gatekeeper al primo avvio; accettabile, si valuta il certificato quando c'è distribuzione vera.
- Windows: differita. La distribuzione decente vuole un certificato di firma e oggi manca la domanda. Gate: primo cliente reale su Windows.

### D6. Aggiornamenti deterministici

- Ogni build stampa dentro il binario origine, versione e commit (ldflags).
- `update` interroga le release della repo di origine, verifica checksum, sostituisce in modo atomico. Rollback = reinstallare la versione precedente.
- Contratto di compatibilità: ogni pacchetto dichiara la versione minima di harness richiesta.
- Update tocca solo ciò che è baked-in. Mai memory, sessioni, config utente.

### D7. Cartelle e precedenze

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

## Architettura

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

### Superficie hook dell'interfaccia estensioni

Il set v0 proposto in rev. 1 era troppo povero. Gruppi di eventi da prevedere nel registro hook (estendibile senza rompere i pacchetti esistenti):

- sessione: start, end, resume, switch;
- ciclo LLM: before_llm_call (ultima chance di mutare messaggi e parametri), after_llm_response, eventi di stream;
- tool: before_tool_call (qui si innesta anche il deny), after_tool_call, errore/retry del tool;
- contesto: assembly del contesto, candidati prune, selezione compact, transform finale prima dell'invio;
- comandi: registrazione comandi custom;
- gateway: messaggio in entrata, risposta in uscita, cambio sessione remota.

Il registro è un enum/versione estensibile: aggiungere hook in sviluppo non è breaking change per le estensioni già scritte.

## Fasi

Solo task, nessuna stima di giorni.

### Fase 0, spike

Prototipo Go che valida peso e velocità del binario e la produttività del team sul linguaggio.

- Loop agentico minimo su OpenRouter, streaming incluso.
- Tool exec e file, sessioni su JSONL.
- Misura RAM a riposo, tempo di startup, dimensione binario, confronto diretto con Pi sugli stessi task.
- Uscita: demo CLI funzionante su un task reale con i numeri a confronto.

### Fase 1, MVP interno

- Gestione contesto smart completa: doppio criterio prune/compact, compact di sicurezza, pin, selezione verbatim via subagent, parametri in config.
- Interfaccia estensioni con il registro hook descritto.
- Provider: solo OpenRouter per l'intero MVP.
- Sessioni JSONL compatibili Pi e comando di import.
- Packaging della repo Digitalygo con i nostri contenuti baked-in.
- Self-update.
- Uscita: il team lavora quotidianamente solo con questo harness, update deterministico provato tra almeno due macchine.

### Fase 2, distribuzione

- Provider estesi post-MVP: Anthropic (API + Pro/Max), OpenAI (API + Codex), DeepSeek, Alibaba Qwen token plan, Moonshot Kimi Coding Plan, Z.ai Coding Plan.
- Pipeline release Linux amd64/arm64 e macOS.
- Brew tap dell'harness base.
- Repo-pacchetto di esempio pubblica come riferimento.
- Uscita: un esterno installa un pacchetto e lavora senza chiedere nulla a noi.

### Fase 3, pacchetti opzionali

- Subagent come pacchetto opzionale.
- Deny di comandi come pacchetto opzionale.
- Uscita: entrambi installabili scegliendo il pacchetto giusto, assenti nella base scarna.

### Fase 4, gateway remoto

- Gateway Telegram con le stesse primitive di sessione della CLI.
- Discord dopo.
- Uscita: giornata di lavoro fatta dal telefono con l'harness che gira sul desktop.

### Fase 5, ecosistema

- Template repo pubblico per bundle compilati (`github.com/digitalygo/smidja-bundle-template`).
- Documentazione per i creatori: bundle compilati e pacchetti di soli contenuti.
- Indice/registry dei pacchetti differito: si crea solo quando la pubblicazione lo richiede.
- Le estensioni restano Go compilate nella build del bundle: nessun meccanismo senza rebuild (subprocess, WASM, plugin). L'opzione "eventuale supporto senza rebuild" è rimossa.
- Uscita: un creatore esterno pubblica il suo pacchetto in autonomia.

## Rischi

- Manutenzione di un harness proprio è un impegno continuo. Mitigazione: scope minimo rigoroso, niente TUI elaborata, osservare le scelte di Pi e di Codex CLI invece di anticiparle tutte.
- Ogni modifica ai contenuti baked-in diventa una release del binario. Mitigazione: layer locale con precedenza per iterare, modalità dev che rilegge i file dal disco, release automatizzate.
- Gli accessi via abbonamento (Pro/Max, Codex, coding plan) hanno flussi OAuth diversi per ogni fornitore e possono cambiando senza preavviso: è lavoro per-integratore da mantenere. Mitigazione: isolare l'autenticazione dal resto dell'integrazione.
- Parità con l'upstream Pi che evolve veloce. Non è un obiettivo: la divergenza mirata (contesto smart nativo, packaging) è il punto.
- Provider minori senza SDK Go: client HTTP+SSE diretto, semplice, non blocca.

## Non obiettivi

App Store e notarizzazione. Windows iniziale. Plugin WASM e subprocess. Server multi-tenant: ogni istanza gira sulla macchina locale. Astrazione formale dei provider, rimandata a un ragionamento dedicato.

## Domande aperte

- Hook: conferma della lista per gruppo ed eventuali eventi extra che vuoi fin da subito.
- Import sessioni Pi: confermato desiderabile; verificare in Fase 0 quanto il formato JSONL di Pi sia stabile da allinearsi.

## Evidenze verificate

- Codex CLI Going Native, motivazioni dichiarate del rewrite da TypeScript: https://github.com/openai/codex/discussions/1174
- Import iniziale codex-rs con rationale (binari standalone, sandbox nativa, niente GC): https://github.com/openai/codex/pull/629
- SDK Go ufficiale OpenAI (rilasci settimanali attivi, v3.x agosto 2026): https://github.com/openai/openai-go
- SDK Go ufficiale Anthropic: https://github.com/anthropics/anthropic-sdk-go
- wazero, runtime WASM puro Go zero dipendenze: https://github.com/tetratelabs/wazero
- Bot framework Telegram per Go maturo: https://github.com/go-telegram/bot
- Coding agent esistente in Go (Charmbracelet Crush): https://github.com/charmbracelet/crush
- Limiti del caricamento dinamico nativo Go (motivo della scelta D3): https://pkg.go.dev/plugin
- Documento estensioni Pi: https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md
- Standard AGENTS.md: https://agents.md/
