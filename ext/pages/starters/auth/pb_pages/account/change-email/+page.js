module.exports = {
  post(ctx) {
    const { email } = ctx.req.body()
    // TODO: request the email change (PocketBase sends a confirmation link to
    // /auth/confirm/{TOKEN}/email-change). Like the 0.x kit, this only echoes
    // the address back for now.
    return { email }
  },
}
