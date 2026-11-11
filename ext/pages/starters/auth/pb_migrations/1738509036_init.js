/// <reference path="../pb_data/types.d.ts" />
migrate(
  (app) => {
    // Send mail to a local maildev (`npx maildev`, web UI on :1080)
    const settings = app.settings()
    const smtp = settings.smtp
    smtp.enabled = true
    smtp.host = 'localhost'
    smtp.port = 1025

    app.save(settings)

    // OAuth2 login is enabled, with no providers. Add yours in the dashboard:
    // Collections > users > Options > OAuth2.
    const users = app.findCollectionByNameOrId('users')
    users.oauth2.enabled = true
    app.save(users)
  },
  (app) => {
    // add down queries...
  }
)
