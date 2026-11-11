type Stats = { notes: number; users: number; now: string; you: string | null }

export function get(ctx: any) {
  const stats: Stats = {
    notes: ctx.app.countRecords('notes'),
    users: ctx.app.countRecords('users'),
    now: new Date().toISOString(),
    you: ctx.auth ? ctx.auth.email() : null,
  }
  return ctx.res.json(stats)
}
