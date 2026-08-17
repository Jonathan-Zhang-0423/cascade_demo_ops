# App Feedback

Use this template only for findings whose `responsibility_domain` is `app` or whose normalized category is `app_package_contract`.

```text
Run/package:
Stage/node:
Finding code:
Severity / decision:
Why the App package is insufficient:
Evidence IDs or artifact IDs:
Reproduction conditions:
Minimum App fix:
Reapproval required: yes/no
Formal App-to-Server acceptance: not complete until rerun with the regenerated package
```

Rules:

- Do not ask Server to edit or fill the approved package.
- Do not include credentials, cookies, tokens, raw HTML, or raw form values.
- Use the exact finding code and evidence IDs from `ValidationRunReport`.
