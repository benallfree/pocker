// Package edge is the shared front of a PocketBase CE edge: the instance model, where instances and edges come from
// (sources), the host that listens and routes requests, and the runtime interface that runs an instance (ext/pocker
// in-process, ext/docker in a container).
package edge

import "context"

// Instance describes one PocketBase app an edge serves.
type Instance struct {
	// ID is a stable, unique identifier. It is also the default folder name under the data root.
	ID string `yaml:"id"`

	// Version is the PocketBase version the instance runs (e.g. "0.41.1" or "0.36.*"); it picks the runtime (see
	// [Runtime.Serves]). Empty = the runtime's default.
	Version string `yaml:"version,omitempty"`

	// Runtime names the runtime that must run the instance ("pocker", "docker"); empty = the first that serves it.
	Runtime string `yaml:"runtime,omitempty"`

	// Hosts are the hostnames (no port) that route to this instance, e.g. "blog.example.com".
	Hosts []string `yaml:"hosts"`

	// Dir overrides the instance folder (default: <data root>/<ID>). The folder holds pb_data, pb_hooks,
	// pb_migrations and pb_public, like a stand-alone PocketBase.
	Dir string `yaml:"dir,omitempty"`

	// Dev runs the app in dev mode (verbose logs, SQL logging).
	Dev bool `yaml:"dev,omitempty"`

	// Secrets are the instance's environment: visible to its JS hooks through $os.getenv and process.env, and to
	// nothing else. No names are reserved; platform settings are explicit fields below.
	Secrets map[string]string `yaml:"secrets,omitempty"`

	// EncryptionKey encrypts the app's stored settings (like `pocketbase serve --encryptionEnv`). Not visible to JS.
	EncryptionKey string `yaml:"encryption_key,omitempty"`

	// Access is the instance owner's network settings (see [Access]).
	Access Access `yaml:"access,omitempty"`

	// Superusers are upserted into the app's _superusers when it starts (in Go, never through env), e.g. so a
	// hosting platform's login works in the dashboard. PasswordHash is the bcrypt hash PocketBase stores.
	Superusers []Superuser `yaml:"superusers,omitempty"`

	// Private holds values for the host application's Go code (see [Server.SetupApp]), e.g. a backup token.
	// It is never visible to the instance's JS.
	Private map[string]string `yaml:"private,omitempty"`

	// Limits override the process defaults for this instance (zero fields keep the default).
	Limits Limits `yaml:"limits,omitempty"`

	// Plugins holds per-instance settings for plugins, by plugin name (e.g. `plugins: {firewall: {rate_limits:
	// ...}}`). A host application's instance source may set typed values; see [DecodePluginConfig]. Changes apply
	// without restarting the instance's app.
	Plugins map[string]any `yaml:"plugins,omitempty"`

	// Blocked, when set, answers every request for this instance with the given status and message instead of
	// serving it (e.g. suspended, unpaid). A running app is left running.
	Blocked *Blocked `yaml:"blocked,omitempty"`

	// Remote, when set, means another server runs this instance (e.g. "http://10.0.0.2:8080"). Pocker applies its
	// firewall (peer check, rate limits) and then forwards the request there instead of starting the app here (see
	// [ManagerOptions.Forward]). An app already running here is stopped.
	Remote string `yaml:"remote,omitempty"`
}

// Access is what the instance's owner controls about who may call it.
type Access struct {
	// CORSOrigins restricts which browser origins may call the instance cross-origin (PocketBase's matching: exact,
	// "*", and "*"/"?" wildcards). Empty = any origin.
	CORSOrigins []string `yaml:"cors_origins,omitempty"`
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

// PluginConfig returns an instance's settings for the named plugin (its `plugins.<name>` value), or nil.
func (i Instance) PluginConfig(name string) any {
	if i.Plugins == nil {
		return nil
	}
	return i.Plugins[name]
}
