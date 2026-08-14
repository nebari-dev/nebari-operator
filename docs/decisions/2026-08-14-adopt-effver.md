# ADR-003: Adopt Effort-based Versioning (EffVer)

| Field       | Value                               |
|-------------|-------------------------------------|
| Date        | 2026-08-14                          |
| Status      | **Accepted**                        |
| Deciders    | NIC maintainers                     |
| Supersedes  | —                                   |

## Context

The operator has shipped twenty pre-releases on the `v0.1.0-alpha.N` line and is preparing its first non-alpha cut, `v0.1.0` (tracked in #129). Before that cut we need a stated versioning policy; the maintainer docs currently point at [Semantic Versioning](https://semver.org/).

SemVer communicates *API compatibility* - a bump says whether the public interface changed in a breaking way. That is a poor fit for where this project is:

1. Pre-1.0, the CRD schema is still moving. Under SemVer nearly every useful change to a `NebariApp` field is a breaking change that would warrant a major bump - so the major number either sprints ahead meaninglessly or we quietly violate the spec on minor bumps, which is effectively what happened across the alpha line.
2. What a consumer actually needs to know - whether a pack author writing `NebariApp` manifests or a maintainer upgrading a cluster - is not "did the API technically break" but "how much work is this upgrade going to be." SemVer does not answer that directly.

[Effort-based Versioning (EffVer)](https://jacobtomlinson.dev/effver/) keeps the same `MACRO.MESO.MICRO` shape but reinterprets each digit as the *effort a consumer should expect when adopting the release*.

## Decision

Adopt EffVer as the operator's versioning policy, starting with `v0.1.0`.

| EffVer component | Meaning for a NebariApp consumer / pack author |
|---|---|
| **MACRO** (`X.y.z`) | "Expect significant changes." CRD shape moved meaningfully; you will touch your pack manifests. |
| **MESO** (`x.Y.z`) | "Could break, watch for warnings." New fields, optional opt-ins, deprecations. |
| **MICRO** (`x.y.Z`) | "Should be safe." Bug fixes, doc updates, operator-internal refactors with no CRD impact. |

Key implementation choices:

- **Tag format is unchanged** - releases stay `vX.Y.Z`, SemVer-shaped, because Helm chart versions and GitHub Releases expect that shape. Only the *interpretation* of the digits changes, and it changes in the docs.
- **The leading `0` still signals early development.** While MACRO is `0`, even a MESO or MICRO bump may cost more effort than the digit implies; EffVer's `0.x` convention says exactly this. That makes the `-alpha.N` pre-release ladder redundant, so we drop it: `v0.1.0` is the first EffVer-governed cut, with no `-alpha`/`-beta` suffix on the stable line. Release candidates (`-rc.N`) may still be used for a pre-cut dry run.
- **The nebari-app library chart inherits the operator's version** (lockstep, per ADR-002). A consumer's chart-upgrade effort is therefore read from the same EffVer digit as the operator upgrade; the chart carries no separate effort signal.
- **Maintainer docs are the source of truth for interpretation** - `release-process.md`, `release-setup.md`, and `release-checklist.md` describe the effort rubric rather than pointing at semver.org.

## Consequences

### Positive

- The version number answers the question consumers actually ask: "do I need to read the release notes, or can I just bump?"
- Honest signal during pre-stable: shipping a routine CRD field change no longer forces a choice between an inflated major number and a spec violation.
- Maps cleanly onto the NebariApp contract: a MACRO bump is precisely "your `NebariApp` manifests may need edits," which is the migration work pack authors care about.

### Negative / Mitigations

| Consequence | Mitigation |
|-------------|------------|
| EffVer is less widely known than SemVer; readers may assume SemVer semantics | Link the spec and carry the rubric table in every maintainer doc that mentions versioning |
| Tooling and consumers may parse `vX.Y.Z` as SemVer and infer a compatibility promise EffVer does not make | Tag shape stays SemVer-valid so tooling keeps working; the compatibility interpretation is documented, and release notes call out MACRO effort explicitly |
| Dropping `-alpha` removes an at-a-glance "not stable yet" cue | The leading `0.` MACRO carries that signal under EffVer, and the README states the pre-1.0 stability caveat in prose |

## Alternatives considered

### A — Effort-based Versioning (EffVer) (chosen)

Reinterpret `MACRO.MESO.MICRO` as adoption effort. Fits a pre-1.0 project with an evolving CRD and a consumer base (pack authors) whose real question is upgrade cost, not API-compatibility theory. Chosen.

### B — Strict Semantic Versioning

Keep semver.org semantics. Rejected: pre-1.0 with a moving CRD, nearly every shippable change is "breaking," forcing either a meaningless major-number sprint or repeated spec violations on minor bumps - the exact drift the alpha line already showed.

### C — Calendar Versioning (CalVer)

Version by date (e.g. `2026.8.0`). Rejected: it communicates recency, not adoption effort or CRD-shape stability, leaving the consumer's core question - "how much work is this upgrade" - unanswered.
