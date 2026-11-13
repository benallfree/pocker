routerAdd("GET", "/hello", (e) => {
  const greet = require(`${__hooks}/lib/greet.js`)
  return e.json(200, { message: greet("embedded hooks"), hooks: __hooks })
})
