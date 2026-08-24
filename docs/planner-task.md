# Planner task: standardize the Smidja decision record into the official Mycelium plan

Act as the planner role and load the `mycelium-plan` skill before anything else.

## Task

Read `docs/decision-record-draft.md` in this repository root. It is a decision record and roadmap draft for Smiðja, a Go agentic coding harness (single static zero-dependency binary, MIT, packages are builds with baked-in content). Standardize that draft into the official Digitalygo implementation plan using the Mycelium plan schema.

## Required schema

Frontmatter exactly:

```yaml
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
```

Body must follow exactly these sections, in order:

1. Current execution snapshot
2. Planner baseline, with subsections: problem statement; research and evidence; hypotheses, decisions, and rationale; planned phases
3. Execution ledger (ledger rules plus empty checkpoint sections for each phase)
4. Plan-variation ledger
5. Closure evidence (empty subsections)

Planned phases must cover all six phases from the draft (spike, MVP interno, distribuzione, pacchetti opzionali, gateway remoto, ecosistema). Each phase needs: objective, planner predictions, proposed steps, predicted verification, completion criterion.

## Critical requirements

- Preserve EVERY decision, constraint, parameter, provider list, precedence order, risk, non-goal, open question, and every evidence URL verbatim from the draft.
- Do not invent new features. Do not drop information.
- Keep Italian language as in the draft.
- Evidence labels: the URLs listed in the draft were verified on 2026-08-23 by the drafting agent; label them accordingly, everything you derive yourself stays a planner prediction.

## Output

Write the plan to `substrate/traces/plans/2026-08-24-smidja-harness-plan.md`, creating directories as needed. Preserve this task document itself; do not modify anything else outside the plan file. Do not run any git commands. When done, print the final path and a one-line summary.
