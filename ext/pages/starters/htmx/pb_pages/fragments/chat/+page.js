// POST /fragments/chat: the form posts a message; index.ejs renders it with the
// chat-msg partial and broadcasts it on the `chat` realtime topic.
// The form uses hx-swap="none", so the (empty) response body is ignored.
module.exports = {
  post(ctx) {
    const { message } = ctx.req.body()
    ctx.data.message = message
  },
}
