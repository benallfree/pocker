package oauthserver

import (
	"errors"
	"fmt"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

// The collections are a contract: ids never change.
const (
	ClientsCollection   = "_oauth_clients"
	ClientsCollectionId = "pbc_oauth_clients"
	CodesCollection     = "_oauth_codes"
	CodesCollectionId   = "pbc_oauth_codes"
)

// Kinds of _oauth_codes rows.
const (
	KindCode    = "code"    // authorization code (single use, ~60 s)
	KindDevice  = "device"  // device code (until approved/denied/expired)
	KindRefresh = "refresh" // refresh token
)

// Device code statuses.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusDenied   = "denied"
)

func clientsCollection() *core.Collection {
	c := core.NewBaseCollection(ClientsCollection, ClientsCollectionId)
	c.System = true
	c.ListRule, c.ViewRule, c.CreateRule, c.UpdateRule, c.DeleteRule = nil, nil, nil, nil, nil
	c.Fields.Add(
		&core.TextField{Id: "oac_client_id", Name: "client_id", Required: true, Max: 200},
		&core.TextField{Id: "oac_name", Name: "name", Max: 200},
		&core.JSONField{Id: "oac_redirect_uris", Name: "redirect_uris", MaxSize: 1 << 14},
		&core.BoolField{Id: "oac_public", Name: "public", Help: "Public clients have no secret and must use PKCE."},
		&core.TextField{Id: "oac_secret_hash", Name: "secret_hash", Hidden: true, Max: 100},
		&core.BoolField{Id: "oac_dynamic", Name: "dynamic", Help: "Registered through /oauth/register."},
		&core.AutodateField{Id: "oac_created", Name: "created", OnCreate: true, System: true},
	)
	c.AddIndex("idx_oauth_clients_client_id", true, "client_id", "")
	return c
}

func codesCollection() *core.Collection {
	c := core.NewBaseCollection(CodesCollection, CodesCollectionId)
	c.System = true
	c.ListRule, c.ViewRule, c.CreateRule, c.UpdateRule, c.DeleteRule = nil, nil, nil, nil, nil
	c.Fields.Add(
		&core.SelectField{Id: "oad_kind", Name: "kind", MaxSelect: 1, Required: true, Values: []string{KindCode, KindDevice, KindRefresh}},
		&core.TextField{Id: "oad_hash", Name: "hash", Required: true, Hidden: true, Max: 100},
		&core.TextField{Id: "oad_user_code", Name: "user_code", Max: 20},
		&core.TextField{Id: "oad_client_id", Name: "client_id", Max: 200},
		&core.TextField{Id: "oad_redirect_uri", Name: "redirect_uri", Max: 2000},
		&core.TextField{Id: "oad_challenge", Name: "challenge", Max: 200},
		&core.JSONField{Id: "oad_scopes", Name: "scopes", MaxSize: 1 << 12},
		&core.TextField{Id: "oad_owner_collection", Name: "owner_collection", Max: 100},
		&core.TextField{Id: "oad_owner", Name: "owner", Max: 100},
		&core.SelectField{Id: "oad_status", Name: "status", MaxSelect: 1, Values: []string{StatusPending, StatusApproved, StatusDenied}},
		&core.TextField{Id: "oad_resource", Name: "resource", Max: 2000, Help: "RFC 8707 resource (token audience)."},
		&core.TextField{Id: "oad_token_id", Name: "token_id", Max: 100, Help: "The access token a refresh token belongs to."},
		&core.DateField{Id: "oad_expires", Name: "expires"},
		&core.DateField{Id: "oad_last_poll", Name: "last_poll"},
		&core.AutodateField{Id: "oad_created", Name: "created", OnCreate: true, System: true},
	)
	c.AddIndex("idx_oauth_codes_hash", true, "hash", "")
	c.AddIndex("idx_oauth_codes_user_code", false, "user_code", "")
	c.AddIndex("idx_oauth_codes_expires", false, "expires", "")
	return c
}

// EnsureSchema creates the OAuth collections that don't exist yet and checks existing ones.
func EnsureSchema(app core.App) error {
	for _, want := range []*core.Collection{clientsCollection(), codesCollection()} {
		have, err := app.FindCollectionByNameOrId(want.Id)
		if err != nil {
			if byName, nameErr := app.FindCollectionByNameOrId(want.Name); nameErr == nil {
				return fmt.Errorf("oauth-server: collection %q exists with id %q, expected %q", want.Name, byName.Id, want.Id)
			}
			if err := app.Save(want); err != nil {
				return fmt.Errorf("oauth-server: create %s: %w", want.Name, err)
			}
			continue
		}
		var errs []error
		added := false
		for _, wf := range want.Fields {
			hf := have.Fields.GetById(wf.GetId())
			if hf == nil {
				have.Fields.Add(wf) // a field a newer version added
				added = true
				continue
			}
			if hf.Type() != wf.Type() || hf.GetName() != wf.GetName() {
				errs = append(errs, fmt.Errorf("field %s (%s)", wf.GetName(), wf.GetId()))
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("oauth-server: %s doesn't match the schema: %w", want.Name, errors.Join(errs...))
		}
		if added {
			if err := app.Save(have); err != nil {
				return fmt.Errorf("oauth-server: add fields to %s: %w", want.Name, err)
			}
		}
	}
	return nil
}

func (s *Server) deleteExpired() error {
	dt, _ := types.ParseDateTime(s.cfg.Now().UTC())
	_, err := s.app.NonconcurrentDB().Delete(CodesCollection,
		dbx.And(dbx.NewExp("expires != ''"), dbx.NewExp("expires < {:now}", dbx.Params{"now": dt.String()}))).Execute()
	return err
}
