// POST /auth/oauth/login (from the login page's provider buttons): stores the
// OAuth2 state in a cookie and redirects to the provider, which sends the user
// back to /auth/oauth/confirm.
module.exports = {
  post(ctx) {
    const { provider } = ctx.req.body()
    try {
      ctx.session.requestOAuth2(provider)
    } catch (e) {
      return { error: e.message }
    }
  },
}
