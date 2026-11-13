package edge

// The host core moved to ext/host (2026-11-13). These names keep existing imports of ext/edge working for one
// release; new code imports ext/host.

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/pockethost/pocketbase/ext/host"
	"gopkg.in/yaml.v3"
)

type (
	Instance         = host.Instance
	Access           = host.Access
	Superuser        = host.Superuser
	Blocked          = host.Blocked
	ChangeKind       = host.ChangeKind
	Change           = host.Change
	InstanceSource   = host.InstanceSource
	Limits           = host.Limits
	QuarantinePolicy = host.QuarantinePolicy
	ByteSize         = host.ByteSize
	Gate             = host.Gate
	TLSProvider      = host.TLSProvider
	Wrapper          = host.Wrapper
	Directory        = host.Directory
	Runtime          = host.Runtime
	Prechecker       = host.Prechecker
	Syncer           = host.Syncer
	Shutdowner       = host.Shutdowner
	Statser          = host.Statser
	Handler          = host.Handler
	TLSConfig        = host.TLSConfig
	TimingEntry      = host.TimingEntry
	Listeners        = host.Listeners
)

const (
	Upsert            = host.Upsert
	Delete            = host.Delete
	DefaultHealthPath = host.DefaultHealthPath
)

var DefaultQuarantine = host.DefaultQuarantine

func ParseByteSize(s string) (ByteSize, error)       { return host.ParseByteSize(s) }
func ChainGates(gates ...Gate) Gate                  { return host.ChainGates(gates...) }
func ValidID(id string) bool                         { return host.ValidID(id) }
func NormalizeHost(h string) string                  { return host.NormalizeHost(h) }
func DevID(h string) string                          { return host.DevID(h) }
func NewDirectory(log *slog.Logger) *Directory       { return host.NewDirectory(log) }
func PeerAddr(r *http.Request) netip.Addr            { return host.PeerAddr(r) }
func TopLevel(doc *yaml.Node, key string) *yaml.Node { return host.TopLevel(doc, key) }
func DecodePluginConfig[T any](v any, out *T) error  { return host.DecodePluginConfig(v, out) }
func WithTimings(ctx context.Context) (context.Context, func() []TimingEntry) {
	return host.WithTimings(ctx)
}
func Timing(ctx context.Context, fn func() []TimingEntry) { host.Timing(ctx, fn) }
func Serve(ctx, base context.Context, l Listeners, handler http.Handler, provider TLSProvider, served func(string) bool, log *slog.Logger, onListen func(addr string)) error {
	return host.Serve(ctx, base, l, handler, provider, served, log, onListen)
}
