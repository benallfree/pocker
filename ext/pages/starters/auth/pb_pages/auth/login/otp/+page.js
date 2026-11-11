// POST /auth/login/otp
//   mode=otp-request: email a one-time code (from the login page, or "Resend")
//   mode=otp-confirm: sign in with the code
module.exports = {
  post(ctx) {
    const body = ctx.req.body()
    const { mode = 'otp-request', otpCode, identity } = body
    let otpId = body.otpId || null
    try {
      switch (mode) {
        case 'otp-request':
          otpId = ctx.session.requestOTP(identity)
          break
        case 'otp-confirm':
          ctx.session.signInWithOTP(otpId, otpCode)
          return ctx.res.redirect('/', { flash: 'Login successful' })
      }
    } catch (e) {
      return { error: e.message, otpId, identity }
    }
    return { otpId, identity }
  },
}
