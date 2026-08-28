# 2048 closed-loop experiment v3

This fixture is the only place where the product name and its specific acceptance floor live. The generic harness selects the workflow from semantic interaction structure, runs one fresh main leg, sends the original one-sentence goal to the target builder, and treats every acceptance criterion in this fixture as required.

The experiment is not valid unless the run owns one newly created entity, records the seven business chapters, performs no out-of-band browser action, and reaches final review only after all generated and assembled media gates pass.

Run it only after the full offline suite passes:

```powershell
.\experiments\2048-v3\run.ps1 -CredentialRef 'credential://demo/<configured-ref>'
```

The script creates a new v3 run with a unique idempotency key, then only polls DemoOps. It never calls the target platform or a media provider directly and stops at the single final-review boundary.

## Run reports

- [`run-ba6b66e-failure-analysis-2026-08-28.md`](reports/run-ba6b66e-failure-analysis-2026-08-28.md): rejected real-run sample and the observe-only recovery regression it exposed.
