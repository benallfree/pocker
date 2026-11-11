// The root layout, in TSX. Every page renders inside it (EJS, Markdown and TSX pages alike).
export default function Layout({ ctx, children, slot, asset }: any) {
  const flash = ctx.flash()
  const nav = [
    ['/', 'Home'],
    ['/jsx', 'JSX/TSX'],
    ['/docs', 'Markdown'],
    ['/notes', 'Notes'],
    ['/clock', 'SSE'],
    ['/stats', 'JSON'],
  ]
  return (
    <html lang="en">
      <head>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <title>{slot('title') || 'PocketPages demo'}</title>
        <link rel="stylesheet" href={asset('assets/app.css')} />
      </head>
      <body>
        <header>
          <a class="brand" href="/">PocketPages</a>
          <nav>
            {nav.map(([href, label]) => (
              <a href={href} class={ctx.req.url.pathname === href ? 'on' : undefined}>{label}</a>
            ))}
          </nav>
          <div class="who">
            {ctx.auth ? (
              <form method="post" action="/auth/logout">
                <span>{ctx.auth.email()}</span> <button>Sign out</button>
              </form>
            ) : (
              <a href="/auth/login">Sign in</a>
            )}
          </div>
        </header>
        {flash && <p class="flash">{String(flash)}</p>}
        <main>{children}</main>
        <footer>
          Rendered by <code>ext/pages</code> in PocketBase CE · page file <code>{ctx.route.file}</code>
        </footer>
      </body>
    </html>
  )
}
