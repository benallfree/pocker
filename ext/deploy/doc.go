// Package deploy deploys a project's code to a PocketBase remote: pb_hooks, pb_migrations, pb_public, packages and
// the production node_modules tree, as a hash diff (manifest → upload only missing files in gzipped PHB1 bundles →
// commit, swapped in safely).
//
// Client half: `pocketbase deploy | dev | pull | logs [remote]` (remotes from ext/remotes). It speaks the same
// protocol as the pockethost npm CLI, so it deploys to PocketHost edges (/_ph/deploy/v1) and to CE remotes
// (/_pocker/deploy/v1), found through /_pocker/whoami.
package deploy
