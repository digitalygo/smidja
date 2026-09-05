---
status: completed
created_at: 2026-09-05
files_edited:
  - digitalygo/smidja-packages/**
  - digitalygo/smidja/README.md
  - digitalygo/smidja/docs/creating-content-packages.md
  - digitalygo/smidja/docs/decision-record-draft.md
  - digitalygo/smidja/substrate/traces/plans/2026-08-24-smidja-harness-plan.md
rationale: Create a low-maintenance public discovery registry now without centralizing package hosting, release metadata, installation, or trust decisions.
supporting_docs:
  - ../plans/2026-08-24-smidja-harness-plan.md
  - https://github.com/digitalygo/smidja-packages
  - https://github.com/digitalygo/smidja-packages/blob/main/docs/registry-format.md
  - https://github.com/digitalygo/smidja/blob/alpha/docs/creating-content-packages.md
---

# Public package registry operation

## Summary of changes

Created and published `digitalygo/smidja-packages` as the public discovery registry for content-only Smidja packages. Updated the harness documentation and living plan to replace the prior registry deferral with the approved publisher-maintained model.

## Technical reasoning

The registry is deliberately separate from package installation. `smidja pkg install owner/repo@version` continues to resolve a public GitHub repository directly, while GitHub tags remain authoritative for versions.

Publishers maintain one stable descriptor through pull requests. The registry never hosts package content, mirrors releases, executes submitted content, crawls GitHub, or certifies package authenticity or safety. A standard-library Go tool strictly validates descriptors and deterministically generates both the machine-readable `index.json` and human-readable `catalog.md`.

Schema version 1 accepts only repository identity, description, maintainers, and discovery topics. Canonical lowercase descriptor paths, size and count limits, strict JSON parsing, Unicode controls, filesystem restrictions, Markdown escaping, read-only CI, CODEOWNERS review, and branch protections bound the public contribution surface.

## Impact assessment

The registry starts with no invented packages and a valid empty catalog. New releases require no registry updates, so maintenance is limited to reviewed listing metadata, ownership changes, and removals submitted by publishers.

No Smidja Go behavior changed, no registry-aware search or installation was introduced, and the compiled-bundle template and Digitalygo reference bundle were untouched. Variation V-019 in the living plan supersedes only V-018's registry deferral. Fase 5 still awaits genuine external creator acceptance.

## Validation steps

- Independently ran formatting, `go mod tidy`, build, vet, shuffled full tests, race tests, and static Linux, macOS, and Windows builds.
- Repeated generation and compared hashes, then verified exact committed outputs with `go run ./cmd/registry check`.
- Validated adversarial JSON, path, symlink, special-file, Unicode, resource-limit, Markdown injection, stale-output, no-write, and CLI behaviors through the mapped tests.
- Measured 95.1% aggregate statement coverage.
- Ran actionlint, YAML parsing, Markdown structure, whitespace, Action pin, and empty-registry contract checks.
- Received quality PASS and focused security PASS for frozen artifact `e81069e16bccea1646a51522fc4b478c7c27144df629fdbd690ebeecbd5b25bb`.
- Verified successful remote CI run 33968792935 on registry commit `5c8ec5f`.
- Verified organization pull-request and CODEOWNERS rules plus repository-required `verify` status, conversation resolution, force-push prevention, and branch-deletion prevention.
- Human-reviewed the documentation-only harness delta at commit `94e4915`; tests and coverage are N/A because that slice contains no executable changes.
