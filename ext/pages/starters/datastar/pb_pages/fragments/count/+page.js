// GET /fragments/count (Datastar @get): bump the counter. index.ejs renders the
// #count card, and the datastar plugin sends it back as a patchElements event
// because the request carries `Datastar-Request: true`.
module.exports = {
  get(ctx) {
    ctx.app.runInTransaction(() => {
      ctx.store('count', (ctx.store('count') || 1) + 1)
    })
  },
}
