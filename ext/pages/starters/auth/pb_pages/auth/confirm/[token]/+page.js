// One handler file for every page in this folder; ctx.route.name tells them
// apart. The mail templates (see pb_migrations) link here:
//   {APP_URL}/auth/confirm/{TOKEN}/verification
//   {APP_URL}/auth/confirm/{TOKEN}/password-reset
//   {APP_URL}/auth/confirm/{TOKEN}/email-change
const message = (e) => String((e && e.message) || e).replace(/^GoError: /, '')

module.exports = {
  load(ctx) {
    if (ctx.route.name === 'verification') {
      try {
        ctx.session.confirmVerification(ctx.params.token)
      } catch (e) {
        throw ctx.httpError(400, 'This verification link is invalid or has expired.')
      }
      return ctx.res.redirect('/', { flash: 'Your account has been verified.' })
    }
    return { error: '' }
  },

  post(ctx) {
    const body = ctx.req.body()
    switch (ctx.route.name) {
      case 'password-reset':
        try {
          ctx.session.confirmPasswordReset(ctx.params.token, body.password, body.passwordConfirm)
        } catch (e) {
          return { error: message(e) }
        }
        return ctx.res.redirect('/auth/login', {
          flash: 'Your password has been changed. Sign in with the new one.',
        })

      case 'email-change': {
        let record
        try {
          record = ctx.session.confirmEmailChange(ctx.params.token, body.password)
        } catch (e) {
          return { error: message(e) }
        }
        const flash = `Your email address is now ${record.email()}.`
        // signed in as this account: the session was renewed with the new address
        if (ctx.auth) return ctx.res.redirect('/account', { flash })
        return ctx.res.redirect('/auth/login', { flash: flash + ' Sign in with it.' })
      }
    }
  },
}
