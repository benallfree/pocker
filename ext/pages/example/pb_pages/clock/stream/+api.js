module.exports = {
  get(ctx) {
    const s = ctx.res.sse()
    for (let i = 1; i <= 10; i++) {
      s.send('tick', `${i}: ${new Date().toISOString()}`)
      sleep(1000)
    }
    s.send('done', 'bye')
    s.close()
  },
}
