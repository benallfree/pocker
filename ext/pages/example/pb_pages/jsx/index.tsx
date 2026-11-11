import Card from '../_private/Card'
import { plural } from '../_private/format'

type Lang = { name: string; ext: string; note: string }

const langs: Lang[] = [
  { name: 'EJS', ext: '.ejs', note: 'Classic templates, compiled to JS in Go.' },
  { name: 'Markdown', ext: '.md', note: 'EJS first, then goldmark.' },
  { name: 'JSX', ext: '.jsx', note: 'Components rendered to HTML on the server.' },
  { name: 'TSX', ext: '.tsx', note: 'Same, with TypeScript types stripped by esbuild.' },
]

export default function JSXDemo({ ctx, slot, include }: any) {
  slot('title', 'JSX and TSX')
  const q: string = ctx.req.query('q') || ''
  const shown = langs.filter((l) => !q || l.name.toLowerCase().includes(q.toLowerCase()))
  return (
    <>
      <h1>JSX and TSX</h1>
      <p>
        This page is <code>jsx/index.tsx</code>. It imports a <code>Card</code> component and a TypeScript helper, and
        renders straight to HTML. No React, no hydration.
      </p>

      <form method="get" class="stack" style={{ marginBottom: '1rem' }}>
        <input name="q" value={q} placeholder="Filter (try: sx)" />
      </form>
      <p class="muted">{plural(shown.length, 'template language')}</p>

      <div class="grid">
        {shown.map((l) => (
          <Card title={l.name} tag={l.ext}>
            <p class="muted">{l.note}</p>
          </Card>
        ))}
      </div>

      <h2>Escaping</h2>
      <p>Text is escaped: {'<script>alert(1)</script>'}</p>
      <p dangerouslySetInnerHTML={{ __html: 'Raw HTML on purpose: <b>bold</b>' }} />

      <h2>Mixing</h2>
      <p>TSX can include an EJS partial:</p>
      {include('badge-card', { href: '/', title: 'From _private/badge-card.ejs', text: 'Locals become variables.' })}
    </>
  )
}
