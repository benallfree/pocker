// GET /fragments/count: bump the counter, then index.ejs renders the new value.
// fragments/ sits outside the (site) group, so no layout wraps the response:
// htmx gets the bare <b>n</b> to swap into the button.
module.exports = {
  get(ctx) {
    ctx.app.runInTransaction(() => {
      ctx.store('count', (ctx.store('count') || 1) + 1)
    })
  },
}
