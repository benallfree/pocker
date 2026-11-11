// GET /fragments/chat (Datastar @get): save the message to the room's
// ephemeral store. index.ejs renders the room's messages and broadcasts them.
module.exports = {
  get(ctx) {
    const { clientId, roomName, from, message } = ctx.datastar.signals()

    ctx.app.runInTransaction(() => {
      const messages = ctx.store(`room_${roomName}`) || []

      const sanitizedMessage = String(message || '')
        .replace(/<script>/g, '')
        .replace(/<\/script>/g, '')
        .trim()
      if (sanitizedMessage.length > 0) {
        messages.push({
          from,
          message: sanitizedMessage,
        })
        ctx.store(`room_${roomName}`, messages)
      }
    })

    ctx.log.debug(`${clientId} sent a message to ${roomName}`)

    return { clientId, roomName, messages: ctx.store(`room_${roomName}`) || [] }
  },
}
