# 2048 one-sentence experiment v4

This experiment tests whether DemoOps can turn the original one-sentence request into a usable product demonstration without silently converting model-inferred aesthetics into user requirements.

Blocking acceptance is limited to a real newly created entity, correct primary merge-game behavior, visible score changes, continued interaction, readable stable layout, responsive usability, and the existing media/content gates. Palette, theme, WASD, undo, touch, and quickly reachable terminal scenes are measured as advisory or enhancement evidence unless the original user sentence explicitly requests them.

The target builder receives only the original sentence. DemoOps may use the internal ProductSpec for evidence planning, but it must not append acceptance details, color instructions, Harness terminology, or media instructions to the target prompt.

Run only after the offline suite passes:

```powershell
.\experiments\2048-v4\run.ps1 -CredentialRef 'credential://demo/<configured-ref>'
```

The script creates exactly one fresh v4 run and only communicates with DemoOps. It never calls the target platform or media providers directly.
