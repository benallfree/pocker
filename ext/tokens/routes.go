package tokens

import (
	"errors"
	"net/http"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/apis"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// TokenView is a token as the API returns it (never the hash).
type TokenView struct {
	Id              string   `json:"id"`
	Name            string   `json:"name"`
	Prefix          string   `json:"prefix"`
	Scopes          []string `json:"scopes"`
	Expires         string   `json:"expires"`
	LastUsed        string   `json:"lastUsed"`
	LastIP          string   `json:"lastIp"`
	ClientId        string   `json:"clientId"`
	Audience        string   `json:"audience"`
	Created         string   `json:"created"`
	OwnerCollection string   `json:"ownerCollection"` // collection name
	Owner           string   `json:"owner"`
	OwnerEmail      string   `json:"ownerEmail,omitempty"`
}

// View converts a token record for API responses.
func (t *Tokens) View(rec *core.Record) TokenView {
	v := TokenView{
		Id:       rec.Id,
		Name:     rec.GetString("name"),
		Prefix:   rec.GetString("prefix"),
		Scopes:   scopesOf(rec),
		Expires:  rec.GetString("expires"),
		LastUsed: rec.GetString("last_used"),
		LastIP:   rec.GetString("last_ip"),
		ClientId: rec.GetString("client_id"),
		Audience: rec.GetString("audience"),
		Created:  rec.GetString("created"),
		Owner:    rec.GetString("owner"),
	}
	if v.Scopes == nil {
		v.Scopes = []string{}
	}
	if col, err := t.app.FindCachedCollectionByNameOrId(rec.GetString("owner_collection")); err == nil {
		v.OwnerCollection = col.Name
		if owner, err := t.app.FindRecordById(col, v.Owner); err == nil {
			v.OwnerEmail = owner.Email()
		}
	}
	return v
}

type createBody struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
	// Expires (RFC 3339) or TTL (seconds); neither: never expires (unless capped).
	Expires string `json:"expires"`
	TTL     int64  `json:"ttl"`
	// OwnerCollection + Owner: superusers may create tokens for any record (collection name or id).
	OwnerCollection string `json:"ownerCollection"`
	Owner           string `json:"owner"`
}

type ownerBody struct {
	OwnerCollection string `json:"ownerCollection"`
	Owner           string `json:"owner"`
}

func (t *Tokens) bindRoutes(se *core.ServeEvent) {
	g := se.Router.Group("/api/tokens")
	g.Bind(apis.RequireAuth())

	g.GET("/scopes", func(e *core.RequestEvent) error {
		return e.JSON(http.StatusOK, map[string]any{
			"scopes":      t.Scopes(),
			"collections": t.cfg.Collections,
			"canCreate":   t.AllowedCollection(e.Auth.Collection()),
		})
	})

	g.GET("", func(e *core.RequestEvent) error {
		var owner *core.Record
		if !e.HasSuperuserAuth() {
			owner = e.Auth
		} else if q := e.Request.URL.Query(); q.Get("owner") != "" {
			o, err := t.findOwner(q.Get("ownerCollection"), q.Get("owner"))
			if err != nil {
				return e.NotFoundError("Owner not found.", err)
			}
			owner = o
		}
		recs, err := t.List(owner)
		if err != nil {
			return e.InternalServerError("", err)
		}
		out := make([]TokenView, 0, len(recs))
		for _, r := range recs {
			out = append(out, t.View(r))
		}
		return e.JSON(http.StatusOK, map[string]any{"items": out})
	})

	g.POST("", func(e *core.RequestEvent) error {
		var body createBody
		if err := e.BindBody(&body); err != nil {
			return e.BadRequestError("Invalid body.", err)
		}
		owner := e.Auth
		if body.Owner != "" && (body.Owner != e.Auth.Id || (body.OwnerCollection != "" &&
			body.OwnerCollection != e.Auth.Collection().Name && body.OwnerCollection != e.Auth.Collection().Id)) {
			if !e.HasSuperuserAuth() {
				return e.ForbiddenError("Only superusers can create tokens for other records.", nil)
			}
			o, err := t.findOwner(body.OwnerCollection, body.Owner)
			if err != nil {
				return e.NotFoundError("Owner not found.", err)
			}
			owner = o
		}
		if !t.AllowedCollection(owner.Collection()) {
			return e.ForbiddenError("Records of "+owner.Collection().Name+" can't own API tokens.", nil)
		}
		// a scoped token can only mint tokens within its own scopes
		if i := FromRequest(e); i != nil && len(i.Scopes) > 0 {
			if len(body.Scopes) == 0 {
				body.Scopes = i.Scopes
			}
			for _, s := range body.Scopes {
				if !i.HasScope(s) {
					return e.ForbiddenError("A scoped token can't create a token with more scopes.", nil)
				}
			}
		}
		opts := CreateOptions{Name: body.Name, Scopes: body.Scopes}
		switch {
		case body.Expires != "":
			exp, err := time.Parse(time.RFC3339, body.Expires)
			if err != nil {
				return e.BadRequestError("expires must be RFC 3339.", err)
			}
			opts.Expires = exp
		case body.TTL > 0:
			opts.Expires = t.cfg.Now().Add(time.Duration(body.TTL) * time.Second)
		}
		secret, rec, err := t.Create(owner, opts)
		if err != nil {
			return e.BadRequestError(err.Error(), nil)
		}
		return e.JSON(http.StatusOK, map[string]any{"token": secret, "record": t.View(rec)})
	})

	g.DELETE("/{id}", func(e *core.RequestEvent) error {
		rec, err := t.Get(e.Request.PathValue("id"))
		if err != nil || !t.mayManage(e, rec) {
			return e.NotFoundError("Token not found.", nil)
		}
		if err := t.app.Delete(rec); err != nil {
			return e.InternalServerError("", err)
		}
		return e.NoContent(http.StatusNoContent)
	})

	g.POST("/revoke-all", func(e *core.RequestEvent) error {
		var body ownerBody
		_ = e.BindBody(&body)
		owner := e.Auth
		if body.Owner != "" && body.Owner != e.Auth.Id {
			if !e.HasSuperuserAuth() {
				return e.ForbiddenError("Only superusers can revoke other records' tokens.", nil)
			}
			o, err := t.findOwner(body.OwnerCollection, body.Owner)
			if err != nil {
				return e.NotFoundError("Owner not found.", err)
			}
			owner = o
		}
		n, err := t.RevokeAll(owner)
		if err != nil {
			return e.InternalServerError("", err)
		}
		return e.JSON(http.StatusOK, map[string]any{"revoked": n})
	})
}

func (t *Tokens) mayManage(e *core.RequestEvent, rec *core.Record) bool {
	if e.HasSuperuserAuth() {
		return true
	}
	return rec.GetString("owner") == e.Auth.Id && rec.GetString("owner_collection") == e.Auth.Collection().Id
}

func (t *Tokens) findOwner(collection, id string) (*core.Record, error) {
	if collection == "" {
		collection = core.CollectionNameSuperusers
	}
	col, err := t.app.FindCachedCollectionByNameOrId(collection)
	if err != nil {
		return nil, err
	}
	if !col.IsAuth() {
		return nil, errors.New("not an auth collection")
	}
	if rec, err := t.app.FindRecordById(col, id); err == nil {
		return rec, nil
	}
	return t.app.FindAuthRecordByEmail(col, id) // an email works too
}
