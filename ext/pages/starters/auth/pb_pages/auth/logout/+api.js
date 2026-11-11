module.exports = {
  load(ctx) {
    ctx.session.signOut()
    return ctx.res.redirect('/')
  },
}
