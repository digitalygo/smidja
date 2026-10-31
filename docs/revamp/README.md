# Manager/client revamp (draft)

This branch implements the approved manager/client architecture incrementally. It is not a deployed upgrade and does not migrate legacy sessions. The existing CLI remains available while the manager is introduced.

## Run

Build `go build ./cmd/smidja`, copy and edit the example configuration, then run:

```
smidja manager --config manager.json
smidja client --config client.json
```

Secrets are environment references, never literal configuration values. Supply a unique manager token (at least 24 characters), a different token for each client, and the Envai API key in the configured environment variables. Open the manager URL in a browser and sign in with its token. Clients expose tools through authenticated outgoing long-poll requests; they do not open an inbound port.

The manager binds to loopback by default. For remote access, configure an HTTPS reverse proxy and `behind_tls_proxy: true`. Do not expose the plain HTTP listener directly. Linux and macOS, amd64 and arm64 are supported; ARMv7 and Windows are not included.

The remote Envai origin must provide ATP v3, native deliveries, cross-Lid delegation, detached background, phase classification, delegated job lifecycle, delegation notification control, and strict model admission. Features are checked; missing capabilities cause explicit failures, not a locally simulated background mode. The adapter targets Envai PR #56 at `c907371b252244cf7a2475d6326e90a28da82880`; that draft is not deployed by this change. No real inference or deployment was exercised while building this change.

## Execution and recovery

- A single manager owns its JSONL state directory, protected by an exclusive process lock.
- Tool intent is flushed before execution and the result before delivery. An interrupted operation with unknown outcome is not executed again automatically.
- The browser file explorer follows the manager/client OS user's permissions, not project roots. It handles text edits with SHA-256 conflict checks, Git status and diff. It is not a sandbox or a full IDE.
- Shell tools receive a small environment allowlist, not manager credentials.
- Verk phases preserve exact outputs. Classification failure pauses the task; explicit retry classifies the saved output without repeating the phase. A classifier retry may incur a second charge if the previous request had an uncertain outcome.
- Blocked tasks resume at their blocked phase with the clarification; prior completed phases remain intact. The originating Smidr chat and turn are pinned even if the user sends other messages.
- Manager restart recovery is best-effort within Envai retention. Sequence gaps and unknown effects are surfaced, not hidden. There is no exactly-once claim.
- Client inventories persist offline without their credentials. Offline capabilities can be inspected but cannot execute.

## Review status

Dedicated offline tests cover the API/CSRF, native contract adapter, signatures, JSONL replay, Verk state, machine tools, client delivery/cancellation and embedded UI behavior. Reproduce JavaScript behavioral checks with `node scripts/test-manager-ui.mjs` (Node only, no dependencies). The separate `Revamp UI` CI workflow installs pinned Playwright and runs `scripts/test-manager-browser.mjs` against offline API fixtures with browser sandboxing enabled, covering desktop and mobile layouts. Local JavaScript syntax and mocked-DOM checks ran. Local Chromium rendering was blocked by the execution environment, but the sandboxed macOS CI smoke passed at e2ff613b. Those are historical results, not evidence for a later revision. The browser suite also produces desktop Tafla, file, Lid and Verk views plus mobile Tafla and file views in the `manager-browser-smoke` artifact. Use the exact-head PR checks and their artifacts for current rendered and visual evidence; see verification.md for the historical scope.

Package installation/updates, private approval-gated configuration, executable extensions, client-owned capabilities and native Lid delegation are implemented and covered by offline tests. Actual cross-repository Envai/Smidja offline runtime integration passed with provider and ownership/catalog fixtures. Live providers and deployment remain unverified; do not treat an installed Go binary as a completed production deployment.

## Initial remote resources

`smidja manager-setup --config setup.json` explicitly registers installation-namespaced client tools, creates the Smidr agent with the requested model, and creates its Lid. It prints IDs and tool bindings to copy into `manager.json`. This mutates the configured Envai workspace/account; merely building or starting this branch does not run setup. Partial failures print the already-created identities and stop; inspect them before retrying to avoid duplicate resources. Setup does not execute inference and cannot verify provider availability. A saved requested model is not proof of the effective model used by an upstream resolver.

### Model identity

The manager defaults are exactly `openai/gpt-6.1-terra` for `ceo_model` and `openai/gpt-6-luna-decisions` for the displayed `classifier_model` preference. `manager-setup` requires an explicit `model`; the setup example uses `openai/gpt-6.1-terra`. Package/private Lid agents carry their own explicit model IDs rather than inheriting an implicit package-wide model.

The Smidr setup records the requested model but cannot prove the provider's live effective model without inference. Smidr messages and Lid/Verk delegations request `strict_model: true`; an incompatible server or unavailable configured root model must reject admission instead of silently selecting a fallback. Provider version suffixes are preserved, not rejected as an alias mismatch. Phase classification is selected by Envai's deployment `DECISIONS_MODEL`, not by a request override. The manager's `classifier_model` is a displayed preference; each phase records the actual returned model, including versioned provider IDs. Changing that preference alone does not reconfigure Envai.

### Private extensions

Approved local extensions execute their literal `command` argv with JSON arguments on stdin, bounded stdout/stderr and process-group cancellation. They do not receive manager credentials by default. An optional `environment` object maps child variable names to environment-variable **names** on the manager, for example `{"ODOO_API_KEY":"ODOO_API_KEY"}`. Missing values fail invocation, not installation. Manager authentication, configured client tokens and Envai authentication variables are not available through this mapping. An archived or superseded definition cannot execute through an old remote tool reference.

Private extensions may target a client by `client_id`. The manager propagates only owner-approved revisions over authenticated, reserved control messages; those controls are not public agent tools. Configuration references resolve on that client, never on the manager. The client saves immutable revision receipts before advertising the new route and retains them across restart. Repeating the same configuration revision is safe; this does not authorize retrying uncertain tool executions.

The configuration page displays activation separately from approval, including offline/unknown clients and clients lacking `state_dir`. An approved definition is not considered active until the online client advertises its exact revision. The manager reconciles every five seconds and retracts stale client definitions when they are archived, moved or removed. New calls to superseded or archived private routes are rejected immediately on the manager, even before client reconciliation. A client processes tool calls serially: an already-running operation may finish before its queued archive is applied, and previous effects are not rolled back. This is best-effort revocation, not a promise of immediate interruption.

### Extension execution timeouts

Private and packaged extension definitions accept optional `timeout_seconds`: omitted or `0` keeps the 30-second default; explicit values from `1` through `600` are supported. The declaration is validated and propagated to Envai tool registration, the manager/client execution path and the subprocess deadline. An earlier parent cancellation or deadline still wins. Arguments supplied by a model cannot change the declared execution budget.

A timeout or cancellation does not undo external side effects. If a result cannot be confirmed, inspect the operation before retrying; Smidja does not blindly rerun the command. Runtime updates preserve the execution settings of package revisions already pinned by a Verk.

## Packages on manager and client

The manager Web UI installs a package from a release-manifest URL, optionally using a `token_env` reference for the source. Installed revisions are immutable and digest-checked. Installation trusts the publisher's future updates: added publisher tools do not introduce another approval gate. Private agent-created extensions remain a separate approval flow. Package dependencies must resolve before capabilities become available; missing runtime environment variables fail invocation, not package installation.

Clients support base machine tools plus installed package tools, Lid/Verk definitions and skills. Configure these optional client fields:

- `state_dir`: local package and approved-extension state directory; required for private client extensions, package sources or an update interval. A client without this field supports base tools only and reports no extension-control capability.
- `package_sources`: array of `{"url":"release-manifest URL","token_env":"OPTIONAL_SOURCE_TOKEN_ENV"}`. Do not reuse the client authentication token for package-source authentication.
- `package_update_interval_seconds`: nonnegative interval; omitted or zero uses 15 minutes. Startup checks immediately, then each interval adds up to 10% jitter. Update failures are printed to client stderr and do not authorize partial activation.

The client installs missing configured sources before connecting, executes package commands locally, and republishes its inventory after package revisions change. Authentication and package-source credential variables are withheld from extension environments. Client runtime errors and missing prerequisites remain explicit; the manager does not install Blender or other host applications for the client.

The manager namespaces client Lid/Verk/skill references by client identity. Only online client capabilities appear in the active catalog for the next Smidr request. Generated remote Lid copies are not independently advertised when their owning client is offline. `clients_list` gives availability; `clients_get` can inspect a remembered offline inventory, while execution still fails offline. Skills are listed without contents and loaded on demand. Selecting which of two capable machines to use remains an instruction to the agent, not an automatic placement policy.

## Private configuration and native Lid

The configuration page shows each agent proposal's exact requested change and current record. Owner approve/reject is separate from agent tools; owner-authored edits apply immediately. Updates and approvals compare revisions, archive is reversible for 30 days, and purge removes expired entities from the active private catalog. Historical JSONL remains audit history, not secure disk erasure. Package entities are not editable through private configuration.

Lid can be discovered and delegated to from the Smidr or the Lid page. The page requires a conversation with an existing native Smidr turn to preserve its origin. Delegation registers a persistent target-session observer and returns a native admission receipt, not a completed result. Native deliveries report results to the originating conversation; this is not a local background-agent substitute. Long-lived observers process future native turns, with durable per-chat cursors and explicit reconnection after stream gaps/errors.

## Owner-controlled client pairing

The Devices page creates a client-specific random credential, displays it once, and offers explicit rotation and revocation. These owner operations use the manager's existing authenticated and CSRF-protected API. Configure the client with its manager URL, client ID, local `state_dir`, and a `token_env` reference whose value is the displayed credential. Never commit the token to source control. The token is cleared when dismissed, when leaving the view, or at logout; delayed responses cannot redisplay a dismissed token.

The manager records only credential hashes and revocation records in its locked, owner-only `client-credentials.jsonl` journal. Successful changes are synchronized to disk before acknowledgement. A lost create response does not authorize regenerating the same ID: creation returns a conflict, and the owner must explicitly rotate that client's credential. Rotation also reconnects a previously revoked identity. Existing static environment-token configuration remains supported, but cannot override a persisted credential or resurrect a revoked ID.

Revocation stops new calls and signals pending operations; already-executed effects are not rolled back. Manager shutdown closes the broker to new authentication and dispatch and ends pending calls, without permanently revoking the saved identities. A new manager instance can load the same journal. A damaged or incomplete journal fails closed and requires inspection rather than silent recovery. Pairing API operations are unavailable when durable credential storage is not configured.

## Product names and compatibility

Smidja is the product. Smidr coordinates Lid and their sequential Verk; Tafla shows task progress. Lid and Verk are invariant names.

These are display names. Existing API routes, tool names, configuration keys, schema kinds and authorization principals remain unchanged, including `ceo_team_id`, `ceo_model`, `team`, `workflow`, `teams_list`, `workflows_list` and `allowed_invokers: ["ceo"]`. JSON editors and manifests intentionally show these technical values. New explicit setup gives the assistant the Smidr description and prompt while retaining installation-namespaced remote agent and team names, which must remain unique. Existing remote resources and saved content are not renamed or migrated automatically.

See [Verk observation and notification recovery](workflow-recovery.md) for retained execution receipts, cancellation and explicit notification redelivery.

## Client service supervision and transport failures

The client currently fails fast: a polling transport error, a heartbeat failure, or an unacknowledged result-delivery error ends the process. It does not contain an automatic reconnect loop. Run it under an operating-system service supervisor if restart-on-failure is required, with backoff and observable logs rather than a tight restart loop. A restarted client registers its current inventory again; being remembered in the manager is not proof that the client is online.

Supervision only restarts the connection process. It must not resubmit a task or replay an uncertain machine operation. The client's completed-result cache is bounded and in memory, so restart does not provide durable exactly-once execution. After an interrupted command or unconfirmed result, inspect the manager's recorded operation and any external effects before deciding whether to retry work. Authentication, revocation and protocol errors require diagnosis rather than repeated blind restarts.
