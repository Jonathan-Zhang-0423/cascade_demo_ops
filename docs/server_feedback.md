# Server Feedback

Use this template for findings whose responsibility is Server execution, Browser Runtime, media evidence, or environment.

```text
Run/package:
Stage/node:
Finding code:
Severity / decision:
Server-owned failure:
Evidence IDs or artifact IDs:
Replay Manifest:
Reproduction conditions:
Minimum Server fix:
Can the original App package be replayed unchanged: yes/no
Formal App-to-Server acceptance: not complete unless the formal package is rerun successfully
```

Rules:

- Separate environment and media-delivery failures from App package defects.
- Do not treat a model/provider candidate as business-action success.
- Do not include credentials, cookies, tokens, raw HTML, or raw form values.
