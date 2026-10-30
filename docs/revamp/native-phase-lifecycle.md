# Native delegated-job lifecycle

Workflow phases require the Envai `delegated_job_lifecycle` capability in
addition to detached execution, native deliveries, cross-team delegation, and
phase classification and suppression of intermediate origin notifications.
Workflow phase delegations send `notify_origin: false`; the classified workflow
result is delivered separately. A deployment without that capability is refused before
phase admission.

Before admitting a phase delegation, Smidja records a local phase conversation
and registers its stream observer. The observer remains alive after the initial
root turn becomes terminal, so subsequent native synthesis turns and their
client-tool requests can still be serviced. Only that observer executes tools;
phase completion does not introduce a second tool consumer.

The delegation's `task_id` identifies the native job. Its initial `turn_id` is
not a completion receipt. `FollowPhase` polls the job through the originating
chat and waits for its authoritative status:

- `running`: wait, even if the initial turn or individual children completed.
- `completed`: retain the exact nonempty final synthesis output.
- `failed` or `cancelled`: stop without classifying or advancing the phase.
- Missing job, unknown status, mismatched identity, missing final output, or a
  stopped observer: pause for explicit recovery, never recreate paid work.

Only an explicit task-cancellation operation requests native subtree
cancellation. Stopping observation, losing a connection, or shutting down the
manager does not cancel remote work. An uncertain remote cancellation remains
visible instead of being reported as successful.
The former local pending-child heuristic has been removed: neither a prompt
instruction nor the observed child count defines job completion.

Offline tests cover admission ordering, retained phase sessions, tool service
after the original terminal, absence of duplicate tool handling, exact phase
payload preservation, initial acknowledgement versus final synthesis, task
identity, stopped observers, unknown outcomes, and cancellation. These fixtures
do not certify that the corresponding Envai branch is deployed or that paid
model calls have been tested live.

The current adapter targets Envai PR #56 commit
`c907371b252244cf7a2475d6326e90a28da82880`. CEO message admission and target
workflow/team delegation explicitly require `strict_model` and request it as
true. An unavailable configured root model must fail before inference rather
than fall back silently. Versioned provider model identifiers are not required
to equal a configuration alias. The provider and deployment have not been
called by these offline tests.
