# Agents

Before writing code, search for similar patterns/logic in the codebase and extract them into
reusable functions. Never duplicate code - if you see existing logic that does what you need,
refactor it into a shared helper first.

before starting to modify files, switch to the main branch and pull.

Before git push run `./scripts/test.sh`.

use UTC when writing dates.

Endpoints which return HTML should either return a complete page XOR return an HTML snippet (for
htmx). If an endpoint returns a snippet, then the endpoint should end with "hx-foo" like
/bar/hx-foo. The corresponding handler should have the prefix "hx", too. Additionally, a template
which contains a snippet should have the prefix "hx-", too. If an endpoints returns JSON, then the
prefix should be "json" for the URL, Go function and Go template.

Before fixing bugs, write a test which reproduces the bug.

When an existing implementation gets changed, then double-check that no old code/scripts/templates
are left. Remove lines which are no longer needed.

Avoid code duplication. If you see the same code pattern in multiple files, extract it into a
reusable component. For Go templates, use `{{define "name"}}...{{end}}` blocks and reference them
with `{{template "name" .}}`.
