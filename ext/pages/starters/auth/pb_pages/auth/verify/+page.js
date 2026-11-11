// Resend the verification email to the signed-in account. Sending is a POST, so
// a link prefetch or a crawler never sends mail.
module.exports = {
  load(ctx) {
    if (!ctx.auth) return ctx.res.redirect('/auth/login', { flash: 'Sign in first.' })
    if (ctx.auth.verified()) return ctx.res.redirect('/account', { flash: 'Your account is already verified.' })
  },
  post(ctx) {
    ctx.session.requestVerification(ctx.auth.email())
    return ctx.res.redirect('/account', {
      flash: `We sent a new verification link to ${ctx.auth.email()}.`,
    })
  },
}
