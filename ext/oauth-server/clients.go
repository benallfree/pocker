package oauthserver

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
)

// Client is a registered OAuth client.
type Client struct {
	Id           string   `json:"client_id"`
	Name         string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
	Public       bool     `json:"public"`
	Dynamic      bool     `json:"dynamic"`
	secretHash   string
	// builtin: the pocketbase CLI (any loopback redirect, device flow)
	builtin bool
}

// CLIClient is the built-in client of `pocketbase login`.
var CLIClient = Client{Id: CLIClientId, Name: "PocketBase CLI", Public: true, builtin: true}

// FindClient returns a client by id: the built-in CLI client, or one from _oauth_clients.
func (s *Server) FindClient(id string) (*Client, error) {
	if id == CLIClientId {
		c := CLIClient
		return &c, nil
	}
	if strings.HasPrefix(id, "https://") {
		return s.cimdClient(id)
	}
	rec, err := s.app.FindFirstRecordByData(ClientsCollectionId, "client_id", id)
	if err != nil {
		return nil, errors.New("unknown client")
	}
	return clientFromRecord(rec), nil
}

func clientFromRecord(rec *core.Record) *Client {
	c := &Client{Id: rec.GetString("client_id"), Name: rec.GetString("name"), Public: rec.GetBool("public"),
		Dynamic: rec.GetBool("dynamic"), secretHash: rec.GetString("secret_hash")}
	_ = rec.UnmarshalJSONField("redirect_uris", &c.RedirectURIs)
	if c.Name == "" {
		c.Name = c.Id
	}
	return c
}

// ListClients returns the registered clients (not the built-in one).
func (s *Server) ListClients() ([]*Client, error) {
	var recs []*core.Record
	if err := s.app.RecordQuery(ClientsCollectionId).OrderBy("created DESC").All(&recs); err != nil {
		return nil, err
	}
	out := make([]*Client, 0, len(recs))
	for _, r := range recs {
		out = append(out, clientFromRecord(r))
	}
	return out, nil
}

// AddClient registers a client. A confidential client (public=false) gets a secret, returned once.
func (s *Server) AddClient(name string, redirectURIs []string, public, dynamic bool) (*Client, string, error) {
	if len(redirectURIs) == 0 {
		return nil, "", errors.New("at least one redirect URI is required")
	}
	for _, u := range redirectURIs {
		if err := validRedirectURI(u); err != nil {
			return nil, "", err
		}
	}
	col, err := s.app.FindCachedCollectionByNameOrId(ClientsCollectionId)
	if err != nil {
		return nil, "", err
	}
	id := "pbc_" + strings.ToLower(security.RandomString(16))
	rec := core.NewRecord(col)
	rec.Set("client_id", id)
	rec.Set("name", strings.TrimSpace(name))
	rec.Set("redirect_uris", redirectURIs)
	rec.Set("public", public)
	rec.Set("dynamic", dynamic)
	secret := ""
	if !public {
		secret = "pbcs_" + security.RandomString(40)
		rec.Set("secret_hash", hashSecret(secret))
	}
	if err := s.app.Save(rec); err != nil {
		return nil, "", err
	}
	return clientFromRecord(rec), secret, nil
}

// RemoveClient deletes a registered client.
func (s *Server) RemoveClient(id string) error {
	rec, err := s.app.FindFirstRecordByData(ClientsCollectionId, "client_id", id)
	if err != nil {
		return fmt.Errorf("no client %q", id)
	}
	return s.app.Delete(rec)
}

func (c *Client) checkSecret(secret string) bool {
	if c.Public {
		return true
	}
	if c.secretHash == "" || secret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashSecret(secret)), []byte(c.secretHash)) == 1
}

// validRedirectURI: absolute, no fragment; https, or http only for loopback; custom schemes (app links) allowed.
func validRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Fragment != "" {
		return fmt.Errorf("invalid redirect URI %q", raw)
	}
	switch u.Scheme {
	case "https":
		if u.Host == "" {
			return fmt.Errorf("invalid redirect URI %q", raw)
		}
	case "http":
		if !isLoopback(u.Hostname()) {
			return fmt.Errorf("redirect URI %q: plain http only for loopback addresses", raw)
		}
	case "javascript", "data", "file":
		return fmt.Errorf("redirect URI %q: scheme not allowed", raw)
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// AllowsRedirect reports whether redirect is acceptable for the client: an exact registered URI, or (RFC 8252) a
// registered loopback URI with any port. The built-in CLI client accepts any http loopback URI.
func (c *Client) AllowsRedirect(redirect string) bool {
	u, err := url.Parse(redirect)
	if err != nil || u.Fragment != "" {
		return false
	}
	if c.builtin {
		return u.Scheme == "http" && isLoopback(u.Hostname())
	}
	if slices.Contains(c.RedirectURIs, redirect) {
		return true
	}
	if u.Scheme != "http" || !isLoopback(u.Hostname()) {
		return false
	}
	for _, reg := range c.RedirectURIs {
		r, err := url.Parse(reg)
		if err != nil || r.Scheme != "http" || !isLoopback(r.Hostname()) {
			continue
		}
		if r.Hostname() == u.Hostname() && r.Path == u.Path && r.RawQuery == u.RawQuery {
			return true
		}
	}
	return false
}
