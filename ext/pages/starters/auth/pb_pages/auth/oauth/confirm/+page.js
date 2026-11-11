// GET /auth/oauth/confirm?state=...&code=... (the provider's redirect)
module.exports = {
  get(ctx) {
    try {
      ctx.session.signInWithOAuth2(ctx.req.query('state'), ctx.req.query('code'))
      return ctx.res.redirect('/')
    } catch (e) {
      return { error: e.message }
    }
  },
}
