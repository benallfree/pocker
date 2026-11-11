# mcp

A [Model Context Protocol](https://modelcontextprotocol.io) endpoint for a PocketBase app, so an agent (a Claude connector, Claude Code, any Streamable HTTP client) can manage the app and call the app's own tools. No sidecar process.

- Built-in `pb_*` tools for collections, records, settings, logs and backups.
- Other plugins add tools from Go. An app's `pb_hooks` adds them with `$mcp.addTool`.
- MCP Streamable HTTP, stateless: each POST carries one JSON-RPC message and gets JSON back. Protocol versions `2025-11-25`, `2025-06-18` and `2025-03-26`. Tools only for now.
- OAuth discovery the way Claude connectors expect it: a `401` with `WWW-Authenticate: Bearer resource_metadata=…`, plus protected resource metadata (RFC 9728) at `/.well-known/oauth-protected-resource[/mcp]`.

A Go port of [pbjsmcp](https://github.com/benallfree/pbjsmcp), with the same tool names and safety rules.

```go
app := pocketbase.New()
server := mcp.MustRegister(app, mcp.Config{})

server.MustAddTool(mcp.Tool{
    Name:        "count_posts",
    Description: "Count published posts.",
    Annotations: mcp.Read,
    Handler: func(c *mcp.Call) (any, error) {
        n, err := c.App.CountRecords("posts", dbx.HashExp{"published": true})
        return map[string]any{"count": n}, err
    },
})
```

A plugin registered on the same app reaches the server with `mcp.For(app)`.

## Sign-in

The endpoint signs requests in through an `Authenticator`.

- **The target:** `ext/oauth-server` + `ext/tokens`. A user pastes `https://<host>/mcp` into Claude's "Add custom connector" (or runs `claude mcp add --transport http <name> https://<host>/mcp`) and signs in to the app in a browser. Tokens are audience-bound to the endpoint and carry the `mcp:read` / `mcp:write` scopes.
- **Until then:** `PocketBaseTokenAuth` accepts ordinary PocketBase auth tokens. By default only superusers can use it, with full scope. A long-lived token for an agent comes from the impersonate API:

```bash
curl -X POST http://127.0.0.1:8090/api/collections/_superusers/impersonate/<SUPERUSER_ID> \
  -H "Authorization: <your superuser token>" -H "content-type: application/json" -d '{"duration": 31536000}'
claude mcp add --transport http my-app http://127.0.0.1:8090/mcp --header "Authorization: Bearer <token>"
```

Scopes: a grant with only `mcp:read` sees only tools marked `readOnlyHint`. Calling any other tool gets `403` with `error="insufficient_scope", scope="mcp:write"`, so the client can ask for more.

## Tools

```go
mcp.Tool{
    Name:         "tool_name",           // 1-128 of A-Za-z0-9_.-, unique per app
    Title:        "Tool name",
    Description:  "What it does, for the model.",
    InputSchema:  map[string]any{...},    // JSON Schema; default: no arguments
    OutputSchema: map[string]any{...},    // optional
    Annotations:  mcp.Read,               // or mcp.Write, mcp.Destroy, or your own
    Allow:        mcp.RequireSuperuser,   // default; mcp.RequireAuth("users"), or any func(*mcp.Call) error
    Handler:      func(c *mcp.Call) (any, error) { ... },
}
```

- `c.Args` holds the arguments. `c.Auth()` is the signed-in record, `c.Grant` holds its scopes and client, and `c.App` and `c.Event` are the app and the request.
- Return any JSON-able value. It's sent as text, and also as `structuredContent` when it's an object.
- Return an error (or panic) to report a tool failure (`isError: true`) that the model can read.
- `Allow` decides who sees the tool at all. A caller it rejects gets "Unknown tool", the same as for a tool that doesn't exist.
- Arguments are not validated against `InputSchema`, so check the ones you rely on.
- `server.AddInstructions(text)` adds a paragraph to the `instructions` sent in `initialize`.

## From JS (pb_hooks)

```go
jsvm.MustRegister(app, jsvm.Config{OnInit: func(vm *goja.Runtime) { mcp.BindJS(vm, app) }})
```

```js
// pb_hooks/mcp.pb.js, at the top level
$mcp.addTool({
  name: 'count_posts',
  description: 'Count published posts.',
  annotations: { readOnlyHint: true },
  allow: 'superuser', // default; "auth" = any signed-in record; ["users"] = those collections
  handler: ({ args, auth, app, e }) => ({ count: app.countRecords('posts', $dbx.hashExp({ published: true })) }),
})
$mcp.instructions('count_posts counts only published posts.')
```

Unlike `routerAdd` handlers, tool handlers can use the file's top-level variables. They run one at a time on the VM that loaded the hooks, so keep them short. Call `$mcp.addTool` only at the top level of a hooks file.

## Built-in tools

They call the app directly, so they bypass collection API rules. Every one requires a superuser.

| Group | Tools |
|---|---|
| `info` | `pb_server_info` |
| `collections` | `pb_list_collections`, `pb_get_collection`, `pb_create_collection`, `pb_update_collection`, `pb_delete_collection`, `pb_import_collections` |
| `records` | `pb_list_records`, `pb_get_record`, `pb_create_record`, `pb_update_record`, `pb_delete_record` |
| `settings` | `pb_get_settings`, `pb_update_settings` |
| `logs` | `pb_list_logs` |
| `backups` | `pb_list_backups`, `pb_create_backup` |

`Config.BuiltinGroups` picks groups, `ReadOnlyBuiltins` keeps only the read-only tools, and `NoBuiltins` drops them all.

Safety:
- `pb_delete_collection` needs `confirm` set to the collection name.
- Imports never delete collections.
- Records come back without hidden fields.
- Settings come back without SMTP and S3 secrets.
- Restoring backups and uploading files are not exposed.

## Config

| Field | Default | |
|---|---|---|
| `Path` | `/mcp` | endpoint path |
| `Name`, `Version`, `Description`, `Icons` | app name slug, `dev` | `serverInfo` (its title is Settings → Application name) |
| `Instructions` | generated | first paragraph of `instructions` |
| `PublicURL` | the request's host | origin used for the OAuth resource URL. The resource must match the URL clients use, so set it when a proxy rewrites the host. |
| `Auth` | `PocketBaseTokenAuth{}` | the `Authenticator` |
| `AllowedOrigins` | none | extra browser origins. Requests with any other `Origin` header get 403. Clients that send no `Origin`, such as Claude's servers and Claude Code, pass. |

## Example

```bash
cd example
go build -o pbmcp .
./pbmcp superuser upsert test@example.com testpassword123 --dir=pb_data
./pbmcp serve --dir=pb_data
```

The example registers `ext/devusers`, so the logins are `admin@example.com` / `password` (superuser) and `user@example.com` / `password` (user). It adds a Go tool (`whoami`) and a JS tool (`recent_users`, in `pb_hooks/mcp.pb.js`).
