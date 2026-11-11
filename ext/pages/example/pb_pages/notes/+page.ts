// Applies to both pages in this folder: notes/index.tsx and notes/[id].ejs.
// ctx.pb() is the PocketBase JS SDK, calling this app in-process as the visitor, so API rules apply.
export function load(ctx: any) {
  const pb = ctx.pb()
  if (ctx.route.name === '[id]') {
    try {
      return { note: pb.collection('notes').getOne(ctx.params.id, { expand: 'author' }) }
    } catch (e) {
      throw ctx.httpError(404, 'No such note')
    }
  }
  return { notes: pb.collection('notes').getFullList({ sort: '-created', expand: 'author' }) }
}

export function post(ctx: any) {
  if (!ctx.auth) return ctx.res.redirect('/auth/login', { flash: 'Sign in to post a note.' })
  const { title, body } = ctx.req.body()
  try {
    ctx.pb().collection('notes').create({ title, body, author: ctx.auth.id })
  } catch (e) {
    return ctx.res.redirect('/notes', { flash: 'Could not save: ' + (e.response?.message || e) })
  }
  return ctx.res.redirect('/notes', { flash: 'Note saved.' })
}
