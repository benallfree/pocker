package pocker

import (
	"context"

	"github.com/pockethost/pocketone/ext/host"
)

// The instance model and the host live in ext/host (shared by every runtime provider); these aliases keep pocker's
// API unchanged.
type (
	Instance       = host.Instance
	Access         = host.Access
	Superuser      = host.Superuser
	Blocked        = host.Blocked
	ChangeKind     = host.ChangeKind
	Change         = host.Change
	InstanceSource = host.InstanceSource
	Limits         = host.Limits
	ByteSize       = host.ByteSize
	Gate           = host.Gate
	TLSProvider    = host.TLSProvider
	Wrapper        = host.Wrapper
	TimingEntry    = host.TimingEntry
	TLSConfig      = host.TLSConfig
	YAMLSource     = host.YAMLSource
)

const (
	Upsert = host.Upsert
	Delete = host.Delete
)

// ParseByteSize parses a size like "512MB" (see [host.ParseByteSize]).
func ParseByteSize(s string) (ByteSize, error) { return host.ParseByteSize(s) }

// WithTimings collects a request's timing entries (see [host.WithTimings]).
func WithTimings(ctx context.Context) (context.Context, func() []TimingEntry) {
	return host.WithTimings(ctx)
}

// Timing registers timing entries for the current request (see [host.Timing]).
func Timing(ctx context.Context, fn func() []TimingEntry) { host.Timing(ctx, fn) }
