/// <reference path="../pb_data/types.d.ts" />
// Demo data: a public "notes" collection (anyone reads, signed-in users write their own) with three notes by
// user@example.com, the local test user that ext/devusers keeps at password "password".
migrate(
  (app) => {
    const users = app.findCollectionByNameOrId('users')
    const notes = new Collection({
      type: 'base',
      name: 'notes',
      listRule: '',
      viewRule: '',
      createRule: '@request.auth.id != "" && @request.body.author = @request.auth.id',
      deleteRule: '@request.auth.id = author',
      fields: [
        { name: 'title', type: 'text', required: true, max: 120 },
        { name: 'body', type: 'text', max: 2000 },
        { name: 'author', type: 'relation', collectionId: users.id, maxSelect: 1 },
        { name: 'created', type: 'autodate', onCreate: true },
      ],
    })
    app.save(notes)

    let demo
    try {
      demo = app.findAuthRecordByEmail('users', 'user@example.com')
    } catch (_) {
      demo = new Record(users)
      demo.setEmail('user@example.com')
      demo.setPassword('password')
      demo.setVerified(true)
    }
    demo.set('name', 'Example User')
    app.save(demo)

    for (const [title, body] of [
      ['Welcome to PocketPages', 'This note came from a migration. Sign in as user@example.com / password to add your own.'],
      ['Pages are files', 'Every .ejs, .md, .jsx and .tsx file in pb_pages is a route.'],
      ['Data is PocketBase', 'This list is read with ctx.pb(), as the visitor, so the collection API rules apply.'],
    ]) {
      const n = new Record(notes)
      n.set('title', title)
      n.set('body', body)
      n.set('author', demo.id)
      app.save(n)
    }
  },
  (app) => {
    app.delete(app.findCollectionByNameOrId('notes'))
  },
)
