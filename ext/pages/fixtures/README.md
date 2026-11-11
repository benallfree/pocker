# pages fixtures

Each folder is a small app (`pb_pages/`) plus `cases.json`: requests and what the response must contain, per
[SPEC.md](../SPEC.md). The `ext/pages` tests start the app (single app, and under pocker with the sandbox), send
every request and check every matcher.

| Matcher | Passes when |
|---|---|
| `status` | the status code is equal |
| `headers` | each header's value is equal |
| `headerContains` | each header contains the substring (`""` = present) |
| `headerAbsent` | each listed header is absent |
| `bodyContains` / `bodyNotContains` | each substring is / isn't in the body |
| `bodyJSON` | the body parses as JSON and is deep-equal |
| `bodyEmpty` | the body is empty |
| `setCookieContains` | each substring is in some `Set-Cookie` header |

Cases run in order against a fresh app per fixture; they don't share cookies.
