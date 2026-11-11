module.exports = (ctx, next) => {
  if (!ctx.auth) {
    return ctx.res.redirect('/auth/login', {
      flash: 'You must be logged in to access this page',
    })
  }
  return next()
}
