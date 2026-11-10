// Package pocker hosts many PocketBase apps in one process.
//
// Requests are routed by Host header to an instance. An instance's PocketBase app is started on its first request and
// then kept running. Where instance information comes from is pluggable through [InstanceSource]: a YAML file for local
// use ([YAMLSource]), or any other provider (a hosting platform's control plane, a database, ...).
package pocker

import "context"

// Instance describes one PocketBase app hosted by Pocker.
type Instance struct {
	// ID is a stable, unique identifier. It is also the default folder name under the data root.
	ID string `yaml:"id"`

	// Hosts are the hostnames (no port) that route to this instance, e.g. "blog.example.com".
	Hosts []string `yaml:"hosts"`

	// Dir overrides the instance folder (default: <data root>/<ID>). The folder holds pb_data, pb_hooks,
	// pb_migrations and pb_public, like a stand-alone PocketBase.
	Dir string `yaml:"dir,omitempty"`

	// Dev runs the app in dev mode (verbose logs, SQL logging).
	Dev bool `yaml:"dev,omitempty"`

	// Env is the instance's own environment, visible to its JS hooks through $os.getenv and process.env.
	// PB_ENCRYPTION_KEY, when set, encrypts the app settings (like `pocketbase serve --encryptionEnv`).
	Env map[string]string `yaml:"env,omitempty"`

	// Superusers are upserted into the app's _superusers when it starts (in Go, never through env), e.g. so a
	// hosting platform's login works in the dashboard. PasswordHash is the bcrypt hash PocketBase stores.
	Superusers []Superuser `yaml:"superusers,omitempty"`

	// Private holds values for the host application's Go code (see [Server.SetupApp]), e.g. a backup token.
	// It is never visible to the instance's JS.
	Private map[string]string `yaml:"private,omitempty"`

	// Limits override the process defaults for this instance (zero fields keep the default).
	Limits Limits `yaml:"limits,omitempty"`

	// Blocked, when set, answers every request for this instance with the given status and message instead of
	// serving it (e.g. suspended, unpaid). A running app is left running.
	Blocked *Blocked `yaml:"blocked,omitempty"`
}

// Superuser is a PocketBase superuser kept in sync by Pocker.
type Superuser struct {
	ID           string `yaml:"id"`
	Email        string `yaml:"email"`
	TokenKey     string `yaml:"token_key"`
	PasswordHash string `yaml:"password_hash"`
}

// Blocked is the response for an instance that must not be served.
type Blocked struct {
	Status  int    `yaml:"status"`
	Message string `yaml:"message"`
}

// ChangeKind is the kind of an instance change.
type ChangeKind int

const (
	// Upsert adds a new instance or replaces an existing one with the same ID.
	Upsert ChangeKind = iota
	// Delete removes the instance with the given ID.
	Delete
)

// Change is one update from an [InstanceSource].
type Change struct {
	Kind     ChangeKind
	Instance Instance // for Delete only Instance.ID is used
}

// InstanceSource provides the instances Pocker serves.
type InstanceSource interface {
	// Snapshot returns the full current list of instances.
	Snapshot(ctx context.Context) ([]Instance, error)

	// Watch streams changes after the snapshot until ctx is done. Sources that never change may return a nil channel.
	Watch(ctx context.Context) (<-chan Change, error)
}
