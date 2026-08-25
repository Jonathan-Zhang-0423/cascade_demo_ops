# Enterprise Film Pipeline — v2 Development Plan

Status: implemented boundary revision; remaining work is limited to renderer integration, fixture A/B acceptance, and authorized provider end-to-end validation.

## Objective

Use H3 and Seedance as complementary presentation generators while preserving the single Browser Agent/OutcomeVerifier fact chain and the existing Server/FinalFilm lifecycle. The design target is high-density, low-coupling creative output: facts are captured once, creative modules consume immutable artifacts, and every fallback is recoverable and auditable.

## Architecture baseline

```text
Browser Agent + OutcomeVerifier
  -> RecordingResultPackage / source binding
  -> CreativeSourceBundle v2 (immutable artifact + digest)
  -> Creative Director Package (bundle binding + shot intents)
  -> ProviderPool (H3 <-> Seedance pre-create fallback)
  -> existing FinalFilm state machine / review / renderer
  -> CreativeQualityProjection + delivery manifest
```

## Implemented contract changes

- `CreativeSourceBundle` v2 records SourceBinding decision/hash, source snapshot digests, recording package lineage, required action coverage, and a canonical digest over the complete normalized bundle.
- `MigrateCreativeSourceBundleV1` is the only supported v1-to-v2 conversion path. It requires explicit lineage and action coverage, regenerates the digest, and never infers missing facts.
- Server-local URIs are separate from `ProviderReference`; only `https://` and `asset://` references can cross into a provider adapter.
- `CreativeDirectorPackage` is the artifact boundary into FinalFilm. FinalFilm receives the package contract, not mutable CreativeSourceBundle state.
- Provider fallback is owned by one ProviderPool implementation. It honors the intent allowlist, runs provider-specific preparation through a hook, switches only before task creation, and resumes an existing task by ID instead of duplicating a charge.
- Editor Compiler emits deterministic transitions and role-safe speed changes without claiming geometry evidence. Zoom/pan requires an immutable artifact with role `target_geometry` and explicit verification.
- Quality is projected from existing FinalFilm/Renderer validation. Provider unavailability is a degradation; missing required action coverage or renderer failures remain blocking.

## Delivery sequence

1. Map Editor Compiler fields to the real renderer timeline while preserving read-only source artifacts, source steps, and time ranges.
2. Run local deterministic-baseline, single-provider, and dual-provider fallback A/B fixtures with the same source bundle.
3. Publish the quality projection through the existing FinalFilm Review Package/Delivery Manifest without changing its state machine, Store, revisions, or final approval logic.
4. Run H3/Seedance end-to-end acceptance only with explicit credentials, authorization, and budget; otherwise stop at preflight and fixtures.

## Non-goals and invariants

- No second fact ledger, fact verification run, Server video state machine, or FinalFilm review state machine.
- Generated shots are presentation-only and cannot replace or rewrite business facts.
- H3 and Seedance are mutual substitutes; if both fail, the deterministic fact baseline remains deliverable with an explicit degradation record.
- A failure must leave evidence sufficient for retry, task recovery, audit, or deterministic fallback.
