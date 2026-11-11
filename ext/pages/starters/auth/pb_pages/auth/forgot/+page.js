module.exports = {
  post(ctx) {
    const { identity } = ctx.req.body()
    // TODO: send the password reset email (PocketBase links it to
    // /auth/confirm/{TOKEN}/password-reset). Like the 0.x kit, the form only
    // re-renders for now.
    return { identity }
  },
}
