module.exports = {
  post(ctx) {
    const { identity, password } = ctx.req.body()
    try {
      ctx.session.register(identity, password)
      return ctx.res.redirect('/')
    } catch (e) {
      return { identity, error: `Error registering user: ${e.message}` }
    }
  },
}
