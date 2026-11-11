module.exports = {
  load(ctx) {
    if (ctx.auth) return ctx.res.redirect('/', { flash: 'Already signed in.' })
    return { error: '', email: 'user@example.com' }
  },
  post(ctx) {
    const { mode, email, password } = ctx.req.body()
    try {
      if (mode === 'register') {
        ctx.session.register(email, password, { verify: false })
        return ctx.res.redirect('/notes', { flash: 'Welcome, ' + email + '!' })
      }
      ctx.session.signInWithPassword(email, password)
      return ctx.res.redirect('/notes', { flash: 'Signed in.' })
    } catch (e) {
      ctx.res.status(400)
      return { error: String(e.message || e).replace(/^GoError: /, ''), email }
    }
  },
}
