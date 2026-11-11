// One handler file for every page in this folder; ctx.route.name tells them
// apart. The mail templates (see pb_migrations) link here:
//   {APP_URL}/auth/confirm/{TOKEN}/verification
//   {APP_URL}/auth/confirm/{TOKEN}/password-reset
//   {APP_URL}/auth/confirm/{TOKEN}/email-change
module.exports = {
  load(ctx) {
    if (ctx.route.name === 'verification') {
      let record
      try {
        record = ctx.app.findAuthRecordByToken(ctx.params.token, 'verification')
      } catch (e) {
        throw ctx.httpError(400, 'This verification link is invalid or has expired.')
      }
      if (!record.verified()) {
        record.setVerified(true)
        ctx.app.save(record)
      }
      return ctx.res.redirect('/', { flash: 'Your account has been verified.' })
    }
  },
}
