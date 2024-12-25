const http = require('http')

const server = http.createServer((req, res) => {
  res.writeHead(200, { 'Content-Type': 'text/plain' })
  res.end('ok')
})

server.listen(8080, () => {
  console.log('Server running on port 8080')
})
server.on('connection', (socket) => {
  const { remoteAddress, remotePort } = socket
  console.log(`New connection from ${remoteAddress}:${remotePort}`)
})
