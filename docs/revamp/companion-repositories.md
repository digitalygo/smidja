# Companion repository alignment

The core manager/client draft does not require a replacement binary from an organization bundle. Publishing this draft does not release packages, update Homebrew, archive Pensum, or change a deployed installation.

Read-only audit on 2026-10-30:

| Repository | Inspected revision | Required follow-up |
| --- | --- | --- |
| smidja-digitalygo | 612f3ef | Convert the alternative sdk.Bundle binary to a runtime archive and release manifest. Reuse existing integration clients through JSON stdin/stdout helpers. |
| smidja-bundle-template | 83492b2 | Replace binary customization/build examples with runtime package authoring, validation and release examples. |
| smidja-packages | 5c8ec5f | Empty discovery index needs no data migration. Update content-only documentation; explicit release source metadata is needed before implementing click-to-install discovery. |
| homebrew-smidja | 5a0d7d3 | Keep the existing source formula until a real authorized release exists. Add manager/client smoke tests, then update only to real release URLs and checksums. |

The implemented package contract uses a ZIP with `smidja.json` at its root, schema version 1, namespaced references, file SHA256 inventory and explicit Linux/macOS amd64/arm64 targets. Package versions are `x.y.z` without a leading `v` or prerelease suffix. The authenticated HTTPS source returns `id`, `version`, `archive_url` and `sha256`. Secrets remain local environment references.

Native helper packages currently need target-specific archives/feeds or a portable launcher selecting an included binary. The runtime does not automatically select one of four naked release executables. A package publisher must not claim otherwise.

Recommended sequence after the core runtime contract is verified: authoring template, organization package, discovery metadata, and finally Homebrew after a real release. No production registry publishing, releases, merges or repository archival are implied by this audit.
