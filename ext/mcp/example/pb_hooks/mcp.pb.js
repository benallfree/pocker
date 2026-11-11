/// <reference path="../pb_data/types.d.ts" />

// Tools from JS: call $mcp.addTool at the top level of a pb_hooks file.

const DEFAULT_LIMIT = 10 // top-level values are visible to tool handlers

$mcp.addTool({
  name: 'recent_users',
  title: 'Recent users',
  description: 'The newest users (email and created date).',
  inputSchema: {
    type: 'object',
    properties: { limit: { type: 'integer', minimum: 1, maximum: 100 } },
    additionalProperties: false,
  },
  annotations: { readOnlyHint: true },
  // allow defaults to "superuser"; "auth" = any signed-in record; ["users"] = those collections
  handler: ({ args, app }) => {
    const rows = app.findRecordsByFilter('users', '', '-created', args.limit || DEFAULT_LIMIT, 0)
    return { users: rows.map((r) => ({ email: r.email(), created: r.getString('created') })) }
  },
})

$mcp.instructions('recent_users lists the newest app users.')
