package oauthserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pockethost/pocketone/ext/tokens"
)

// What Claude's connector flow needs (private maintainer plan for ext/mcp, Auth).
func TestClaudeConnectorFlow(t *testing.T) {
	var docURL string
	doc := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := docURL
		if r.URL.Path == "/wrong" {
			id = "https://elsewhere.example/x"
		}
		json.NewEncoder(w).Encode(map[string]any{"client_id": id, "client_name": "Claude Code",
			"redirect_uris": []string{"http://localhost/callback", "http://127.0.0.1/callback"}, "token_endpoint_auth_method": "none"})
	}))
	defer doc.Close()
	docURL = doc.URL + "/oauth/claude-code-client-metadata"

	e := newEnv(t, Config{CIMDHosts: []string{"127.0.0.1"}, HTTPClient: doc.Client()})
	e.tk.RegisterScope(tokens.Scope{Name: "mcp:read", Description: "Read-only MCP tools", Paths: []string{"/mcp"}})

	// metadata
	res, _ := (&http.Client{Transport: &http.Transport{}}).Get(e.url + MetadataPath)
	var m map[string]any
	json.NewDecoder(res.Body).Decode(&m)
	res.Body.Close()
	if m["client_id_metadata_document_supported"] != true || m["registration_endpoint"] != nil ||
		!strings.Contains(strings.Join(toStrings(m["scopes_supported"]), " "), "offline_access") ||
		!strings.Contains(strings.Join(toStrings(m["token_endpoint_auth_methods_supported"]), " "), "none") {
		t.Fatalf("metadata %v", m)
	}
	e.srv.EnableDynamicRegistration()
	res, _ = (&http.Client{Transport: &http.Transport{}}).Get(e.url + MetadataPath)
	json.NewDecoder(res.Body).Decode(&m)
	res.Body.Close()
	if m["registration_endpoint"] != e.url+"/oauth/register" {
		t.Fatal("DCR not enabled at runtime")
	}

	verifier, challenge := pkce()
	resource := e.url + "/mcp"
	redirect := "http://localhost:51234/callback"
	q := url.Values{"response_type": {"code"}, "client_id": {docURL}, "redirect_uri": {redirect}, "state": {"s"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "scope": {"mcp:read offline_access"}, "resource": {resource}}
	b := newBrowser(t)
	b.get(e.url + "/oauth/authorize?" + q.Encode())
	b.submit("password", map[string]string{"email": "test@example.com", "password": "1234567890"})
	if !b.has("Claude Code") || !b.has("mcp:read") || b.has("<b>offline_access</b>") {
		t.Fatalf("consent: %s", b.page)
	}
	r := b.submit("allow", nil)
	loc, _ := url.Parse(r.Header.Get("Location"))
	if loc.Host != "localhost:51234" || loc.Query().Get("iss") != e.url {
		t.Fatalf("redirect %s", loc)
	}
	st, tok := postToken(t, e.url, url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
		"redirect_uri": {redirect}, "client_id": {docURL}, "code_verifier": {verifier}, "resource": {resource}})
	if st != 200 || tok["scope"] != "mcp:read" {
		t.Fatalf("token %d %v", st, tok)
	}
	info, err := e.tk.Verify(tok["access_token"].(string))
	if err != nil || info.Audience != resource {
		t.Fatalf("audience %v %+v", err, info)
	}
	call := func(token, path string) int {
		req, _ := http.NewRequest(http.MethodGet, e.url+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := (&http.Client{Transport: &http.Transport{}}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := call(tok["access_token"].(string), "/api/collections"); code != 403 {
		t.Fatalf("MCP token on the records API: %d", code)
	}
	// refresh keeps the audience; the old pair dies in the same response
	st, tok2 := postToken(t, e.url, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {docURL}})
	if st != 200 {
		t.Fatalf("refresh %d %v", st, tok2)
	}
	if info, _ := e.tk.Verify(tok2["access_token"].(string)); info == nil || info.Audience != resource {
		t.Fatal("audience lost on refresh")
	}
	if st, o := postToken(t, e.url, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {docURL}}); st != 400 || o["error"] != "invalid_grant" {
		t.Fatalf("dead refresh token: %d %v", st, o)
	}

	// resource on another host, untrusted CIMD host, mismatched document
	q.Set("resource", "https://other.example.com/mcp")
	if r := b.get(e.url + "/oauth/authorize?" + q.Encode()); !strings.Contains(r.Header.Get("Location"), "error=invalid_target") {
		t.Fatalf("foreign resource: %s", r.Header.Get("Location"))
	}
	q.Set("resource", resource)
	q.Set("client_id", "https://evil.example.com/meta")
	if b.get(e.url + "/oauth/authorize?" + q.Encode()); b.status != 400 {
		t.Fatalf("untrusted CIMD host: %d", b.status)
	}
	q.Set("client_id", doc.URL+"/wrong")
	if b.get(e.url + "/oauth/authorize?" + q.Encode()); b.status != 400 {
		t.Fatalf("mismatched document: %d", b.status)
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
