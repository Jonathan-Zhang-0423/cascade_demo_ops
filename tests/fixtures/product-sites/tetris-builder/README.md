# Tetris Builder Product Fixture

Small route-aware product site for App-side three-in-one package tests.

Business flow:

- `/login`: demo account login.
- `/app`: workspace with `data-testid="new-project"`, project name input, build mode selector, and start build button.
- `/project/demo-tetris`: project detail page that shows the agent build log for a Tetris app.

The fixture intentionally uses stable business selectors and route-shaped page states so the App agent can generate concise `approval_markdown`, `stage_approval_plan`, and `script_outline` without scanning unrelated files.
