// GET /boom throws: the log and the error name pb_hooks/main.pb.js:4, not pb.js
routerAdd("GET", "/boom", (e) => {
  const order = null
  return e.json(200, { total: order.total })
})
