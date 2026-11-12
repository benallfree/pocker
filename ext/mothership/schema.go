// Package mothership is the control plane of a fleet of edges: a PocketBase app that records which edges exist and
// which instances run where. Edges follow it with the [mirror] package. One operator, no users: owners, accounts
// and plans are an extension (a multitenant plugin adds its collections and fields here, and a mirror extension on
// the edges).
package mothership

import (
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// EnsureCollections creates the `edges` and `instances` collections when they are missing (fields an extension
// added are left alone). Only superusers can read or write them; edges log in as superusers.
//
// edges:     edge_id (unique name, e.g. lax-1), internal_url, region, status (online|draining|offline), last_seen
// instances: name (unique slug; <name>.<apex> routes to it), hosts (extra hostnames), edge (placement), power (off =
//
//	not served), suspension (non-empty = blocked with this message), dev, version, secrets, encryption_key,
//	access, limits, plugins, status
func EnsureCollections(app core.App) error {
	edges, err := app.FindCollectionByNameOrId("edges")
	if err != nil {
		edges = core.NewBaseCollection("edges")
		edges.Fields.Add(
			&core.TextField{Name: "edge_id", Required: true},
			&core.TextField{Name: "internal_url"},
			&core.TextField{Name: "region"},
			&core.TextField{Name: "status"},
			&core.DateField{Name: "last_seen"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		edges.AddIndex("idx_edges_edge_id", true, "edge_id", "")
		if err := app.Save(edges); err != nil {
			return err
		}
	}
	if _, err := app.FindCollectionByNameOrId("instances"); err == nil {
		return nil
	}
	inst := core.NewBaseCollection("instances")
	inst.Fields.Add(
		&core.TextField{Name: "name", Required: true},
		&core.JSONField{Name: "hosts"},
		&core.RelationField{Name: "edge", CollectionId: edges.Id, MaxSelect: 1},
		&core.BoolField{Name: "power"},
		&core.TextField{Name: "suspension"},
		&core.BoolField{Name: "dev"},
		&core.TextField{Name: "version"},
		&core.JSONField{Name: "secrets", Hidden: true},
		&core.TextField{Name: "encryption_key", Hidden: true},
		&core.JSONField{Name: "access"},
		&core.JSONField{Name: "limits"},
		&core.JSONField{Name: "plugins"},
		&core.TextField{Name: "status"},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	inst.AddIndex("idx_instances_name", true, "name", "")
	inst.AddIndex("idx_instances_edge", false, "edge", "")
	return app.Save(inst)
}
