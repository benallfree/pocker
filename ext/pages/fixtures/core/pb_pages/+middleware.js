module.exports = (ctx, next) => {
  ctx.data.trail = ['root']
  return next()
}
