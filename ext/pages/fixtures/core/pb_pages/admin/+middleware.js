module.exports = (ctx, next) => {
  ctx.data.trail.push('admin')
  if (ctx.req.query('key') !== 'open') return ctx.res.redirect('/login')
  return next()
}
