package oauthserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Client ID metadata documents (draft-ietf-oauth-client-id-metadata-document): an https client id is the URL of a
// JSON document {client_id, client_name, redirect_uris}. Claude publishes one (Claude Code's is
// https://claude.ai/oauth/claude-code-client-metadata). Only Config.CIMDHosts are trusted.

const cimdTTL = 10 * time.Minute

type cimdEntry struct {
	client *Client
	at     time.Time
}

func (s *Server) cimdClient(id string) (*Client, error) {
	u, err := url.Parse(id)
	if err != nil || u.Scheme != "https" || u.Fragment != "" || u.User != nil || !slices.Contains(s.cfg.CIMDHosts, strings.ToLower(u.Hostname())) {
		return nil, errors.New("unknown client")
	}
	if v, ok := s.cimd.Load(id); ok {
		if e := v.(cimdEntry); time.Since(e.at) < cimdTTL {
			return e.client, nil
		}
	}
	req, err := http.NewRequest(http.MethodGet, id, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := s.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client metadata: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("client metadata: HTTP %d", res.StatusCode)
	}
	var doc struct {
		ClientId     string   `json:"client_id"`
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("client metadata: %w", err)
	}
	if doc.ClientId != id {
		return nil, errors.New("client metadata: client_id doesn't match the document URL")
	}
	if doc.AuthMethod != "" && doc.AuthMethod != "none" {
		return nil, errors.New("client metadata: only public clients (token_endpoint_auth_method none) are supported")
	}
	var uris []string
	for _, r := range doc.RedirectURIs {
		if validRedirectURI(r) == nil {
			uris = append(uris, r)
		}
	}
	if len(uris) == 0 {
		return nil, errors.New("client metadata: no usable redirect_uris")
	}
	c := &Client{Id: id, Name: doc.ClientName, RedirectURIs: uris, Public: true}
	if c.Name == "" {
		c.Name = u.Hostname()
	}
	s.cimd.Store(id, cimdEntry{client: c, at: time.Now()})
	return c, nil
}
