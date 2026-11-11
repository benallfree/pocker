module.exports = {
  load(ctx) {
    ctx.data.loaded = 'yes'
    if (ctx.params.id === 'missing') throw ctx.httpError(404, 'No such product')
    return { name: 'Widget ' + ctx.params.id }
  },
  get(ctx) {
    ctx.data.method = 'get'
  },
  post(ctx) {
    const body = ctx.req.body()
    return ctx.res.redirect(`/products/${ctx.params.id}`, { flash: 'Saved ' + body.qty })
  },
}
