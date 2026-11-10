# Composable Verk

The embedded UI uses the approved v14 design as its visual reference. Its data comes from the manager APIs. The mock's simulated executions, accounts, files and success messages are not included.

Existing workflows with `phases` remain supported. Composable workflows use `schema_version: 2` and a `graph` with `schema_version: 1`, nodes, ports and edges. A run executes one branch at a time. The saved graph is the executed definition; there is no separate canvas-only executable model.

## Authoring and execution

Use the Verk catalog to create a private draft, edit blocks and connections, configure inputs/outputs and validate before starting. Owner saves use existing private configuration revision checks. Package definitions remain immutable; create a private copy to edit one. Incomplete drafts may be saved, but runnable validation rejects incomplete connections and invalid trigger configuration.

Blocks include entry, Lid, registered tool, nested Verk, data mapping, deterministic IF/switch, finite-choice AI, clarification, approval, wait, bounded repetition and exit. Declared input defaults are applied before schema validation and saved with the task. Runtime inputs use typed literal/reference operands. References address `inputs`, `variables` and `nodes.<id>.<field>`. Missing required references and type mismatches stop the run explicitly. No expression executes JavaScript or arbitrary code.

Deterministic conditions support AND, OR, NOT, XOR, NAND, NOR, XNOR and ONE. XOR is odd parity, XNOR even parity, ONE exactly one match. AND/OR short-circuit left to right; evaluated invalid operands fail closed. Ordered branches choose the first matching branch, otherwise their configured default. Rule limits are 32 levels and 512 nodes.

AI choices are explicit labels through Envai's authenticated `classify-choice` endpoint. The model cannot invent a destination or call tools. A provider/transport error pauses the run; uncertain paid work is never silently repeated. Native Lid jobs finish only after their children and final coordinator synthesis, independently of a Smidr conversation.

## Persistence and recovery

Each block admission, remote handle, full output, route and gate transition is recorded in the manager JSONL journal. Output is retained before output-schema validation, so a failed validation does not discard evidence or rerun the tool. Known completed tool receipts are reused after recovery. An unknown effect remains paused, including after restart; resuming is not permission to repeat it blindly.

Native admission rejection, including a billing HTTP 402, closes its chat observer and remains closed after restart. Pre-turn billing notices retain their event cursor without being mistaken for an admitted turn. An uncertain mutation outcome, including transport failure, timeout, conflict or server error after submission, retains observation because admission may have succeeded; none triggers automatic resubmission.

Private manager tools require the approved revision and exact payload to remain active when the block executes. Archiving or updating the extension revokes an older task snapshot. Completed receipts remain reusable without executing the extension again. Older snapshots without a private revision pin fail closed; start a new task from the current definition. Immutable package tool pins remain usable after a package update.

Nested definitions and registered tool versions are pinned to the task. Nested depth is limited to eight and an admission snapshot to 512 definitions; recursive definition cycles are rejected. Graph cycles must pass through a bounded repetition edge. A run also has a hard limit of 10,000 block visits. Exhausted loop budgets require an explicit owner grant, limited to 100 additional iterations per action and a cumulative hard limit of 10,000.

Clarification and approval gates retain their exact block request and gate IDs. Approval also binds the proposal digest; stale or repeated answers are rejected. Silence and timeout never imply approval. Wait gates accept a matching authenticated source event or their configured timeout. A blocked exit can be explicitly resumed without rerunning finished work.

Cancelling stops the task's observers and active tool contexts, requests cancellation for retained native jobs and children, and prevents late child admission. Remote failure remains visible as unconfirmed cancellation. Already produced effects cannot be undone.

## Triggers

A configured entry can accept Smidr invocation, signed GitHub push/PR/completed-CI events, signed custom events or a five-field cron schedule with an IANA timezone. Saving configuration does not provision GitHub subscriptions or create credentials. Repository and branch scopes must be explicit; organization scope matches only events authenticated with the configured webhook secret.

The manager accepts machine events at `POST /hooks/verk`. Set `SMIDJA_WEBHOOK_SECRET` in the manager's configured environment resolver to enable ingress. It is protected from extension environment inheritance. Missing configuration returns 503; invalid authentication returns 401. Payloads are limited to one MiB.

GitHub uses its standard `X-Hub-Signature-256` signature and delivery/event headers. Custom senders use `X-Smidja-Source`, `X-Smidja-Delivery` and `X-Smidja-Signature-256: sha256=<hex>`, signing the UTF-8 source, newline, delivery ID, newline, then the exact request body with HMAC-SHA256. Delivery conflicts and payload replay are checked against persisted tasks. A single source event cannot advance two consecutive wait gates.

The scheduler evaluates the current minute while the manager runs; it does not replay a shutdown backlog. A repeated daylight-saving local minute does not launch a duplicate run. Automatic tasks stay silent until their configured notification or intervention. At that point, one Smidr conversation is created for the root execution and reused, including nested gates. User-originated tasks reuse their original conversation. Native notifications can be retried with the same event identity from Tafla.

## Browser behavior

Files and diff are separate substates. Selecting a file opens its editor, including on mobile. Selection is bound to its original machine/path/digest; a delayed response cannot replace a newer selection. Save conflicts preserve local edits and require explicit reload or comparison. Git views support selected tracked, staged, renamed and untracked files and display output truncation.

The graph viewport has a readable minimum zoom, touch targets of at least 44 px and a drag threshold separating movement from opening the inspector. No provider credentials are stored in the browser. Authentication, CSRF checks, bounded tool output and text-safe rendering remain in force.

## Envai requirements

All native ATP requests opt into `X-Envai-Extensions: native-tasks-v1`. Graph execution additionally requires `independent_jobs`, `finite_choice_classification` and `originless_deliveries` for the corresponding operations. Strict model selection remains mandatory for native jobs and Smidr notifications. Older servers fail capability checks before the unsupported operation. These contracts are developed in Envai draft PR 56; no deployment is implied by this branch.
