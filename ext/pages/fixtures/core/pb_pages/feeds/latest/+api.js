module.exports = {
  get(ctx) {
    return ctx.res.json({ items: [1, 2, 3], q: ctx.req.query('q') ?? null })
  },
}
