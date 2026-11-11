---
title: 'Speedrun: Google OAuth2'
description: Set up Google OAuth2 sign-in for a PocketPages app with the auth plugin.
---

# Google OAuth2 Setup Guide

This guide walks you through setting up Google OAuth2 sign-in for your PocketPages app.

## 1. Create/Select Google Cloud Project

1. Go to [Google Cloud Console](https://console.cloud.google.com)
2. Either select an existing project or create a new one
3. If creating new, provide a project name when prompted

![Creating a new Google Cloud project](2025-02-08-06-54-56.png)
![Naming the new project](2025-02-08-06-56-00.png)
![Selecting the project](2025-02-08-06-56-28.png)

## 2. Configure OAuth Credentials

1. Navigate to "Credentials" in the left sidebar
2. Click "Create Credentials" and select "OAuth client ID"

![Navigating to credentials](2025-02-08-06-56-56.png)
![Creating OAuth credentials](2025-02-08-06-57-07.png)

### Configure Application Type

1. Select "Web application" as the application type
2. Configure your authorized origins and redirect URIs:
   - **Authorized JavaScript origins**: Your application domain (e.g., `http://localhost:8090`)
   - **Authorized redirect URIs**: Your auth callback URL (e.g., `http://localhost:8090/auth/oauth/confirm`)

![Selecting web application](2025-02-08-06-57-57.png)
![Configuring URIs](2025-02-08-07-00-42.png)

### Configure Consent Screen (if needed)

If you haven't set up a consent screen before, you'll need to configure it:

1. Choose External/Internal user type
2. Fill in the required application information

![Consent screen setup](2025-02-08-06-57-19.png)
![Consent screen details](2025-02-08-06-57-27.png)

### Download Credentials

1. After creating the OAuth client, download the JSON credentials file
2. Store this file securely - it contains sensitive information

![Downloading credentials](2025-02-08-06-58-27.png)
![Credentials JSON format](2025-02-08-06-58-37.png)

## 3. Configure PocketBase OAuth2

1. Open your PocketBase Admin UI
2. Make sure your site URL is set correctly. It must match the `redirect URL` domain you configured in the Google Cloud Console.
3. Navigate to Settings > Auth Providers
4. Enable OAuth2 authentication
5. Add Google as a new provider

![PocketBase site URL](2025-02-08-15-28-44.png)
![PocketBase user settings](2025-02-08-06-59-00.png)
![OAuth2 providers section](2025-02-08-06-59-09.png)
![Adding new provider](2025-02-08-06-59-17.png)
![Selecting Google provider](2025-02-08-06-59-24.png)

### Provider Configuration

1. Enter your Client ID and Client Secret from the downloaded credentials
2. Configure any additional auth options as needed
3. Save your changes

![Configuring Google provider](2025-02-08-06-59-32.png)
![Saving changes](2025-02-08-06-59-59.png)

## Customizing the Return URL

By default, the `auth` plugin uses `/auth/oauth/confirm` as the OAuth2 return path. Change it when you start the flow:

```js
ctx.session.requestOAuth2('google', { redirectPath: '/custom/oauth/callback' })
```

Remember to add the new redirect URL to your Google OAuth2 configuration too.

## Implementation Guide

### 1. Turn on the auth plugin

```js
// pb_pages/+config.js
module.exports = {
  plugins: ['auth'],
}
```

### 2. Login form

```html
<form method="post" action="/auth/oauth">
  <input type="hidden" name="provider" value="google" />
  <button type="submit">Sign in with Google</button>
</form>
```

### 3. Start the flow

```js
// pb_pages/auth/oauth/+page.js
module.exports = {
  post(ctx) {
    ctx.session.requestOAuth2(ctx.req.body().provider)
  },
}
```

`requestOAuth2` stores its state in a cookie and redirects the user to Google's consent screen.

### 4. Callback

```js
// pb_pages/auth/oauth/confirm/+page.js
module.exports = {
  load(ctx) {
    try {
      ctx.session.signInWithOAuth2(ctx.req.query('state'), ctx.req.query('code'))
    } catch (e) {
      return { error: e.message }
    }
    return ctx.res.redirect('/')
  },
}
```

```ejs
<!-- pb_pages/auth/oauth/confirm/index.ejs -->
<mark><%%= data.error %></mark>
```

### 5. Migration

Set up Google OAuth2 in a PocketBase migration:

```js
migrate(
  (app) => {
    const users = app.findCollectionByNameOrId('users')
    users.oauth2.enabled = true
    users.oauth2.providers = [
      {
        name: 'google',
        clientId: 'YOUR_CLIENT_ID',
        clientSecret: 'YOUR_CLIENT_SECRET',
      },
    ]
    app.save(users)
  },
  (app) => {
    const users = app.findCollectionByNameOrId('users')
    users.oauth2.enabled = false
    users.oauth2.providers = []
    app.save(users)
  }
)
```

### 6. Protected routes

```js
// pb_pages/account/+middleware.js
module.exports = (ctx, next) => {
  if (!ctx.auth) return ctx.res.redirect('/login')
  return next()
}
```

### 7. Auth state in the UI

```ejs
<%% if (ctx.auth) { %>
  <div>Welcome, <%%= ctx.auth.email() %></div>
  <form method="post" action="/logout"><button>Log out</button></form>
<%% } else { %>
  <a href="/login">Log in</a>
<%% } %>
```

### 8. Logout

```js
// pb_pages/logout/+page.js
module.exports = {
  post(ctx) {
    ctx.session.signOut()
    return ctx.res.redirect('/')
  },
}
```

See [Authentication](/docs/authentication) and the [auth plugin](/docs/plugins/auth).

## Troubleshooting

Common issues and solutions:

- **Invalid redirect URI**: Ensure your redirect URI exactly matches what's configured in Google Cloud Console
- **Consent screen not showing**: Make sure you've properly configured the OAuth consent screen
- **Authentication failed**: Verify your client ID and secret are correctly copied to PocketBase

## Security Considerations

- Always use HTTPS in production
- Keep your client secret secure
- Regularly rotate credentials
- Review Google OAuth2 access regularly in your Google Cloud Console
