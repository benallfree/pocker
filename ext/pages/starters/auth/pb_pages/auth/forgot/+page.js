module.exports = {
  post(ctx) {
    const { identity } = ctx.req.body()
    const email = String(identity || '').trim()
    if (!email.includes('@')) {
      return { identity: email, error: 'Enter the email address you signed up with.' }
    }
    // PocketBase emails a link to /auth/confirm/{TOKEN}/password-reset (see pb_migrations).
    // It never says whether the address has an account.
    ctx.session.requestPasswordReset(email)
    return ctx.res.redirect('/auth/login', {
      flash: `If ${email} has an account, a password reset link is on its way.`,
    })
  },
}
