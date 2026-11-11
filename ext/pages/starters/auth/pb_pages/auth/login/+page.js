// The login page shows every auth method the users collection supports.
// Each section's form posts here with a `mode`; errors are shown in the
// section that posted.
module.exports = {
  load(ctx) {
    const users = ctx.app.findCollectionByNameOrId('users')
    return {
      methods: {
        password: { enabled: users.passwordAuth.enabled },
        otp: { enabled: users.otp.enabled },
        mfa: { enabled: users.mfa.enabled },
        oauth2: {
          enabled: users.oauth2.enabled,
          providers: users.oauth2.providers.map((p) => ({
            name: p.name,
            displayName: p.displayName || p.name,
          })),
        },
      },
      errors: {},
    }
  },
  post(ctx) {
    const { mode, identity, password } = ctx.req.body()
    try {
      switch (mode) {
        case 'anonymous':
          ctx.session.signInAnonymously()
          return ctx.res.redirect('/')
        case 'password':
          ctx.session.signInWithPassword(identity, password)
          return ctx.res.redirect('/')
      }
    } catch (e) {
      ctx.log.error(`login (${mode}) failed`, e)
      return { identity, errors: { [mode]: e.message } }
    }
  },
}
