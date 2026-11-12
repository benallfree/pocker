package pocker

import "github.com/pockethost/pocketbase/ext/edge"

// The instance model lives in ext/edge (shared by every runtime); these aliases keep pocker's API unchanged.
type (
	Instance         = edge.Instance
	Access           = edge.Access
	Superuser        = edge.Superuser
	Blocked          = edge.Blocked
	ChangeKind       = edge.ChangeKind
	Change           = edge.Change
	InstanceSource   = edge.InstanceSource
	Limits           = edge.Limits
	QuarantinePolicy = edge.QuarantinePolicy
	ByteSize         = edge.ByteSize
)

const (
	Upsert = edge.Upsert
	Delete = edge.Delete
)

// ParseByteSize parses a size like "512MB" (see [edge.ParseByteSize]).
func ParseByteSize(s string) (ByteSize, error) { return edge.ParseByteSize(s) }

// DefaultQuarantine is the built-in memory quarantine policy (see [edge.DefaultQuarantine]).
var DefaultQuarantine = edge.DefaultQuarantine
