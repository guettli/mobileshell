# Agents

after changes run `./scripts/test.sh`.

use UTC when writing dates.

Endpoints which return HTML should either return a complete page XOR return an HTML snippet (for
htmx). If an endpoint returns a snippet, then the endpoint should end with "hx-foo" like /bar/hx-foo.
The corresponding handler should have the prefix "hx", too. Additionally, a template which
contains a snippet should have the prefix "hx-", too.

Before fixing bugs, write a test which reproduces the bug.
