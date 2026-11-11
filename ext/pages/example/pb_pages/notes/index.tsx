import { plural, when } from '../_private/format'

export default function Notes({ ctx, data, slot }: any) {
  slot('title', 'Notes')
  const notes: any[] = data.notes
  return (
    <>
      <h1>Notes</h1>
      <p class="muted">
        {plural(notes.length, 'note')}, read with <code>ctx.pb()</code> as{' '}
        {ctx.auth ? ctx.auth.email() : 'a guest'}.
      </p>

      {ctx.auth ? (
        <form method="post" class="stack card" style={{ marginBottom: '1.25rem' }}>
          <input name="title" placeholder="Title" required maxlength={120} />
          <textarea name="body" rows={3} placeholder="Say something" />
          <button>Post note</button>
        </form>
      ) : (
        <p>
          <a href="/auth/login">Sign in</a> to post (demo@example.com / demo12345).
        </p>
      )}

      <div class="grid">
        {notes.map((n) => (
          <a class="card" href={`/notes/${n.id}`} style="text-decoration:none;color:inherit">
            <h3>{n.title}</h3>
            <div class="muted">{n.body}</div>
            <div class="badge" style={{ marginTop: '.5rem' }}>
              {n.expand?.author?.name || n.expand?.author?.email || 'someone'} · {when(n.created)}
            </div>
          </a>
        ))}
      </div>
    </>
  )
}
