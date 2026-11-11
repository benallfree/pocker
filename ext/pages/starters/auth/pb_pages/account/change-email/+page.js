module.exports = {
  post(ctx) {
    const email = String(ctx.req.body().email || '').trim()
    try {
      // PocketBase emails the new address a link to /auth/confirm/{TOKEN}/email-change.
      ctx.session.requestEmailChange(email)
    } catch (e) {
      return { email, error: String(e.message || e).replace(/^GoError: /, '') }
    }
    return ctx.res.redirect('/account', {
      flash: `We sent a confirmation link to ${email}. Your address changes when you open it.`,
    })
  },
}
