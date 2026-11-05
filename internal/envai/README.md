# Envai adapter

Verified against `digitalygo/envai-teams` alpha commit
`36489c47a4c3e4c874f9f3a793432d3ad06b3d3d`:

- `docs/agent-team-protocol.md`
- `docs/guides/client-tools.md`
- `apps/control-plane/routes/api.php`
- `apps/control-plane/app/Http/Requests/v1/Agent/StoreAgentRequest.php`
- `apps/control-plane/app/Actions/v1/Team/IndexTeamAction.php`

`Config.BaseURL` is the origin, without `/api/v1`. Bearer credentials stay in
this server-side adapter; redirects are not followed. ATP requests require an
explicit version 3 response acknowledgement. Every ATP request sends `X-Envai-Extensions: native-tasks-v1`, including capability discovery, initial subscriptions, reconnects, turn admission and tool-result submission. Resource and JWKS requests do not carry this ATP opt-in. Control-plane resource methods
use the documented `data` envelope. `ListTeams` uses the current unpaginated
owner-scoped endpoint.

Persist the local intention before `CreateChat` or `StartTurn`, then persist
the returned remote identifiers before `FollowTurn`. Mutations are never
retried: transport failures can have an uncertain remote outcome. Streaming
reconnects use the last successfully handled sequence; gaps and resets require
explicit recovery. Replay is not durable history. A completed turn without a
recoverable orchestrator final output is an error, not an empty successful
answer. `FollowTurn` preserves the exact final text without trimming.

`VerifyToolRequest` verifies Ed25519 using same-origin JWKS and pinned RFC 8785
canonicalization. Its signature only authenticates the call ID, tool name,
and arguments. The manager must additionally bind the authenticated chat,
tracked turn, allowed tool/agent, package revision, and device; durably record
call receipts before side effects and never rerun an uncertain call. A valid
signature is not permission to execute arbitrary tools.

Native detached execution, cross-Lid result ingress, and generic Decisions
are not inferred from the legacy `background_delegation` capability. The
native methods use separate `detached_background`, `native_deliveries`,
`cross_team_delegation`, and `phase_classification` capability gates. Their
contract is coordinated with the in-progress Envai branch, not the alpha
baseline above; deployment must expose these features before use. This adapter
does not emulate them with local queues or user-message injection.

Classification request IDs are single-use. A 502/503 or lost response can mean
the paid invocation ran: pause, retain the phase output, and require explicit
recovery. A user-requested retry uses a new ID and the same phase output, never
rerunning the phase. A task lookup returning 404 does not prove nonexecution.
Delivery retry is safe only for the same event ID and identical payload while
the server retains its receipt; resident-only receipts are not durable proof
after restart.

The pinned `github.com/cyberphone/json-canonicalization` dependency is the
upstream RFC 8785 implementation, Apache-2.0, at commit `32a48195b9b2`.
All adapter tests use local HTTP fixtures; no paid or live model requests are
part of these checks.

The native lifecycle baseline was verified against Envai commit
`c907371b252244cf7a2475d6326e90a28da82880`,
`packages/contracts/smidja-native.json` (PR 56, not yet deployed).
`StartTurn` requires `strict_model` and sends `strict_model: true`, so an
unavailable configured Smidr model fails before inference rather than falling
back. Native origin wake turns inherit that policy. Manager Verk and
direct Lid delegations also request strict target-root model selection;
the low-level delegation input retains the optional flag for explicit policy.
Children retain Envai's own model-resolution policy. Provider model aliases or
version suffixes are diagnostic data, not evidence of a failed strict request.

Verk readiness additionally requires `delegated_job_lifecycle` and
`delegation_notify_origin`. A phase suppresses automatic origin notification,
keeps the target SSE observer alive, and reads the entire delegated job's
terminal output. Its initial coordinator turn acknowledgement is not phase
completion; `result_turn_id` can identify a later synthesis turn. Only the
Verk's final or blocked result explicitly wakes the Smidr.
