# Workflow input and invocation contract

Definitions can declare `input_schema` and `allowed_invokers`. The installed package or approved private definition is authoritative; an agent cannot replace its policy or schema through tool arguments. Each task pins the resolved definition, including its schema and invocation policy. Resume checks that snapshot, not a newer package version.

## Bounded schema vocabulary

This is a bounded JSON-Schema-compatible subset, not full Draft 2020-12. Supported keywords:

- `type`: a single string: object, array, string, number, integer, boolean, null.
- `properties`, `required` (unique strings), `additionalProperties` (boolean only).
- `items` (schema object), `minItems`, `maxItems`.
- `minLength`, `maxLength` (Unicode code points).
- `minimum`, `maximum`, `enum`, `const`, `title`, `description`.

Unknown keywords including `$schema`, `$ref`, `pattern` and `anyOf` are rejected. No schema network requests occur. Schemas must be objects, at most 64 KiB and 32 nested schema levels. Inputs are limited to 1 MiB. Exact rational number comparison supports numeric representations up to 4096 characters and absolute decimal exponents up to 4096. Numbers outside this domain are rejected recursively, including in otherwise unconstrained inputs and enum/const values. Length and item bounds are nonnegative integers fitting int64.

Omitted schemas preserve permissive `{}` behavior within these input bounds. Invalid schemas fail at private/package admission; invalid input fails before task persistence or phase dispatch.

## Invocation policy

The authenticated owner UI/API can invoke any owned workflow, still subject to its input schema. Agent invocations derive identity from durable manager-recorded chat/team provenance, never from prompt text or supplied arguments. Missing provenance fails closed.

`allowed_invokers` accepts `ceo` and `team:<stable team_ref>`. This is team-level authorization, not a per-specialist ACL. Client package team references are scoped to their client identity when exposed by the manager. `ceo` means the configured CEO team, including native child work executed in that team's chat.

- Absent or null: owner plus explicitly tool-provisioned CEO.
- Empty array: owner only.
- Explicit list: owner plus listed teams.

Non-CEO callers must invoke an approved catalog workflow, not an inline definition authorizing themselves. The CEO retains the previously provisioned inline-workflow capability; persistent private definitions still require approval. The built-in read-only PR review explicitly permits the CEO.

Invocation deduplication uses the trusted chat ID plus call ID. Full phase outputs remain unchanged; policy does not replace workflow context or classifiers.
