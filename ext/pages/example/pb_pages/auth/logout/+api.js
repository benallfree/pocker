module.exports = {
  post(ctx) {
    ctx.session.signOut()
    return ctx.res.redirect('/', { flash: 'Signed out.' })
  },
}
