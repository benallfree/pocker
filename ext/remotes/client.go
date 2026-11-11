package remotes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// UserAgent is sent with every client request.
var UserAgent = "pocketbase-cli"

// NewHTTPClient returns the HTTP client remote commands use: short connect and TLS timeouts, no overall timeout
// (uploads and log streams run long; callers pass contexts).
func NewHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 2 * time.Minute, // a commit may restart the instance
		MaxIdleConnsPerHost:   4,
		ForceAttemptHTTP2:     true,
	}}
}

// Client talks to one remote with its credential. Get one with [Open], or build it directly.
type Client struct {
	Remote *Remote
	// Token is sent as "Authorization: Bearer <token>" (empty: anonymous).
	Token string
	// TokenSource says where Token came from, for messages ("POCKETBASE_TOKEN", "saved login", ...).
	TokenSource string
	HTTP        *http.Client
}

// ErrNotLoggedIn is returned by [Open] when there's no credential for the remote.
var ErrNotLoggedIn = errors.New("not logged in")

// Open resolves a remote of the project containing dir (name may be empty or a URL, see [Project.Resolve]) and its
// credential: the CI environment first ([EnvToken]), then the saved login. With no credential it returns the
// client and an error wrapping [ErrNotLoggedIn].
func Open(dir, name string) (*Client, error) {
	p, err := LoadProject(dir)
	if err != nil {
		return nil, err
	}
	r, err := p.Resolve(name)
	if err != nil {
		return nil, err
	}
	c := &Client{Remote: r, HTTP: NewHTTPClient()}
	if tok, src := EnvToken(r.Name); tok != "" {
		c.Token, c.TokenSource = tok, src
		return c, nil
	}
	creds, err := LoadCredentials()
	if err != nil {
		return nil, err
	}
	if cr := creds.ByURL[r.URL]; cr != nil && cr.Token != "" {
		c.Token, c.TokenSource = cr.Token, "saved login ("+cr.Via+")"
		if !cr.Expires.IsZero() && time.Now().After(cr.Expires) {
			return c, fmt.Errorf("the saved login for %s expired: run 'pocketbase login %s': %w", r.URL, r.Name, ErrNotLoggedIn)
		}
		return c, nil
	}
	return c, fmt.Errorf("%s (%s): run 'pocketbase login %s', or set POCKETBASE_TOKEN: %w", r.Name, r.URL, loginArg(r), ErrNotLoggedIn)
}

func loginArg(r *Remote) string {
	if r.Name == r.URL {
		return r.URL
	}
	return r.Name
}

// URL joins the remote's base URL and path.
func (c *Client) URL(path string) string {
	return c.Remote.URL + "/" + strings.TrimLeft(path, "/")
}

// NewRequest builds an authenticated request to path on the remote.
func (c *Client) NewRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.URL(path), body)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("User-Agent", UserAgent)
	return req, nil
}

// Do sends a request with the client's HTTP client.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	h := c.HTTP
	if h == nil {
		h = NewHTTPClient()
		c.HTTP = h
	}
	res, err := h.Do(req)
	if err != nil {
		return nil, &APIError{Status: 0, Message: fmt.Sprintf("could not reach %s: %v", c.Remote.URL, err)}
	}
	return res, nil
}

// APIError is a non-2xx answer (Status 0: the remote couldn't be reached).
type APIError struct {
	Status  int
	Message string
	Body    map[string]any
}

func (e *APIError) Error() string { return e.Message }

// IsStatus reports whether err is an [APIError] with this status.
func IsStatus(err error, status int) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == status
}

// JSON sends in (nil: no body; []byte: raw octet stream) and decodes a 2xx answer into out (may be nil).
func (c *Client) JSON(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	ctype := ""
	switch v := in.(type) {
	case nil:
	case []byte:
		body, ctype = bytes.NewReader(v), "application/octet-stream"
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		body, ctype = bytes.NewReader(b), "application/json"
	}
	req, err := c.NewRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return DecodeResponse(res, out)
}

// DecodeResponse turns a non-2xx response into an [APIError] (PocketBase's {message} or the deploy API's), else
// decodes JSON into out.
func DecodeResponse(res *http.Response, out any) error {
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		ae := &APIError{Status: res.StatusCode, Body: map[string]any{}}
		if json.Unmarshal(b, &ae.Body) == nil {
			ae.Message, _ = ae.Body["message"].(string)
		} else if s := strings.TrimSpace(string(b)); s != "" {
			ae.Message = clip(s, 300)
		}
		if ae.Message == "" {
			ae.Message = fmt.Sprintf("HTTP %d", res.StatusCode)
		}
		return ae
	}
	if out == nil || len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// Whoami is GET /_pocker/whoami.
type Whoami struct {
	Auth *struct {
		Collection string `json:"collection"`
		Id         string `json:"id"`
		Email      string `json:"email"`
	} `json:"auth"`
	// Via: "token" (API token), "jwt", or "" (anonymous).
	Via     string   `json:"via"`
	Scopes  []string `json:"scopes"`
	TokenId string   `json:"tokenId"`
	// Features map a feature to where it lives: "deploy" → "/_pocker/deploy/v1", "tokens" → "/api/tokens",
	// "oauth" → "/.well-known/oauth-authorization-server".
	Features map[string]string `json:"features"`
	App      struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"app"`
}

// ErrNoWhoami: the remote has no /_pocker/whoami (no remotes plugin, e.g. a PocketHost edge today).
var ErrNoWhoami = errors.New("the remote doesn't offer /_pocker/whoami")

// Whoami asks the remote who this client is and what it offers.
func (c *Client) Whoami(ctx context.Context) (*Whoami, error) {
	var w Whoami
	err := c.JSON(ctx, http.MethodGet, WhoamiPath, nil, &w)
	if IsStatus(err, http.StatusNotFound) {
		return nil, ErrNoWhoami
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}
