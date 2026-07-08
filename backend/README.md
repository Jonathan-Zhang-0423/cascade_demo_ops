# Cascade DemoOps Go Backend

This backend follows `docs/requirements/project-initialization-v2.md`.

The MVP source of truth is `DemoWorkflowGraph`. The backend owns the linear orchestration flow:

1. InputCtx
2. ProductExplore
3. GraphGenerate
4. HumanApprove
5. ExecuteRehearse
6. AssetGenerate

Run shape:

```powershell
go run . --mode=desktop --product-url=http://localhost:3000 --local-repo-path=D:\path\to\repo
go run . --mode=web --product-url=https://staging.example.com --git-repo-url=https://github.com/org/repo.git
```

Agent logic is intentionally stubbed at this stage. Playwright/Remotion work belongs to `video-worker` through JSON-RPC 2.0 over stdio.
