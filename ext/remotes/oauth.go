package remotes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// OAuthClientId is the built-in public client of ext/oauth-server.
const OAuthClientId = "pocketbase-cli"

// OAuthMetadata is the part of RFC 8414 metadata the CLI uses.
type OAuthMetadata struct {
	Issuer                      string `json:"issuer"`
	AuthorizationEndpoint       string `json:"authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	RevocationEndpoint          string `json:"revocation_endpoint"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

func init() { BrowserLogin = OAuthLogin }

// FetchOAuthMetadata reads the remote's authorization server metadata (path from whoami's "oauth" feature).
func FetchOAuthMetadata(ctx context.Context, c *Client, path string) (*OAuthMetadata, error) {
	anon := *c
	anon.Token = ""
	var m OAuthMetadata
	if err := anon.JSON(ctx, http.MethodGet, path, nil, &m); err != nil {
		return nil, fmt.Errorf("OAuth metadata: %w", err)
	}
	if m.TokenEndpoint == "" || m.AuthorizationEndpoint == "" {
		return nil, errors.New("OAuth metadata is missing endpoints")
	}
	return &m, nil
}

// canOpenBrowser: false over SSH or on a Linux box without a display.
func canOpenBrowser() bool {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return false
	}
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	return true
}

// OpenBrowser opens url in the user's browser (overridable for tests).
var OpenBrowser = func(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		return exec.Command("xdg-open", u).Start()
	}
}

// OAuthLogin signs in through the remote's OAuth server: the browser with a loopback redirect and PKCE, or a
// device code (opts.Device, or when no browser can be opened here).
func OAuthLogin(ctx context.Context, c *Client, w *Whoami, opts BrowserLoginOptions) (*LoginResult, error) {
	out := opts.Out
	if out == nil {
		out = io.Discard
	}
	md, err := FetchOAuthMetadata(ctx, c, w.Features["oauth"])
	if err != nil {
		return nil, err
	}
	var tr *tokenResponse
	if opts.Device || (!opts.NoBrowser && !canOpenBrowser()) {
		if md.DeviceAuthorizationEndpoint == "" {
			return nil, errors.New("the remote doesn't offer device sign-in; use --token")
		}
		tr, err = deviceFlow(ctx, c, md, out)
	} else {
		tr, err = loopbackFlow(ctx, c, md, opts, out)
	}
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	cred := &Credential{Token: tr.AccessToken, Via: "oauth", RefreshToken: tr.RefreshToken, Created: now,
		TokenEndpoint: md.TokenEndpoint, RevokeEndpoint: md.RevocationEndpoint}
	if tr.ExpiresIn > 0 {
		cred.Expires = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	probe := *c
	probe.Token = tr.AccessToken
	if who, err := probe.Whoami(ctx); err == nil && who.Auth != nil {
		cred.Email, cred.Collection = who.Auth.Email, who.Auth.Collection
	}
	return &LoginResult{Credential: cred}, nil
}

func randomURLString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func loopbackFlow(ctx context.Context, c *Client, md *OAuthMetadata, opts BrowserLoginOptions, out io.Writer) (*tokenResponse, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	verifier := randomURLString(48)
	sum := sha256.Sum256([]byte(verifier))
	state := randomURLString(24)

	q := url.Values{
		"response_type": {"code"}, "client_id": {OAuthClientId}, "redirect_uri": {redirect}, "state": {state},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
	}
	authURL := md.AuthorizationEndpoint + "?" + q.Encode()

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		qs := r.URL.Query()
		var res result
		switch {
		case qs.Get("state") != state:
			res.err = errors.New("sign-in failed: state mismatch")
		case qs.Get("error") != "":
			res.err = fmt.Errorf("sign-in failed: %s %s", qs.Get("error"), qs.Get("error_description"))
		default:
			res.code = qs.Get("code")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		msg := "Signed in. You can close this tab and go back to the terminal."
		if res.err != nil {
			msg = "Sign-in didn't finish: " + res.err.Error()
		}
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>PocketBase</title><body style="font:16px system-ui;display:flex;align-items:center;justify-content:center;height:90vh"><p>%s</p></body>`, htmlEscape(msg))
		select {
		case done <- res:
		default:
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()

	fmt.Fprintf(out, "Opening the browser to sign in to %s.\nIf it doesn't open, visit:\n  %s\n", c.Remote.URL, authURL)
	if !opts.NoBrowser {
		_ = OpenBrowser(authURL)
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var res result
	select {
	case <-wait.Done():
		return nil, errors.New("timed out waiting for the browser sign-in (5 minutes)")
	case res = <-done:
	}
	if res.err != nil {
		return nil, res.err
	}
	return tokenRequest(ctx, c, md.TokenEndpoint, url.Values{
		"grant_type": {"authorization_code"}, "code": {res.code}, "redirect_uri": {redirect},
		"client_id": {OAuthClientId}, "code_verifier": {verifier},
	})
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

func deviceFlow(ctx context.Context, c *Client, md *OAuthMetadata, out io.Writer) (*tokenResponse, error) {
	var da struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := postForm(ctx, c, md.DeviceAuthorizationEndpoint, url.Values{"client_id": {OAuthClientId}}, &da); err != nil {
		return nil, fmt.Errorf("device sign-in: %w", err)
	}
	fmt.Fprintf(out, "To sign in to %s, open\n  %s\non any device and enter the code  %s\n", c.Remote.URL, da.VerificationURI, da.UserCode)
	if da.VerificationURIComplete != "" {
		fmt.Fprintf(out, "(or open %s)\n", da.VerificationURIComplete)
	}
	interval := time.Duration(max(da.Interval, 1)) * time.Second
	deadline := time.Now().Add(time.Duration(max(da.ExpiresIn, 60)) * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		tr, err := tokenRequest(ctx, c, md.TokenEndpoint, url.Values{
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {da.DeviceCode}, "client_id": {OAuthClientId},
		})
		var oe *oauthErr
		switch {
		case err == nil:
			return tr, nil
		case errors.As(err, &oe) && oe.code == "authorization_pending":
		case errors.As(err, &oe) && oe.code == "slow_down":
			interval += 5 * time.Second
		case errors.As(err, &oe) && oe.code == "access_denied":
			return nil, errors.New("sign-in was denied")
		case errors.As(err, &oe) && oe.code == "expired_token":
			return nil, errors.New("the code expired; run login again")
		default:
			return nil, err
		}
	}
	return nil, errors.New("the code expired; run login again")
}

type oauthErr struct{ code, desc string }

func (e *oauthErr) Error() string {
	if e.desc != "" {
		return e.code + ": " + e.desc
	}
	return e.code
}

func postForm(ctx context.Context, c *Client, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		var e tokenResponse
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return &oauthErr{e.Error, e.Description}
		}
		return &APIError{Status: res.StatusCode, Message: fmt.Sprintf("HTTP %d: %s", res.StatusCode, clip(strings.TrimSpace(string(b)), 200))}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

func tokenRequest(ctx context.Context, c *Client, endpoint string, form url.Values) (*tokenResponse, error) {
	var tr tokenResponse
	if err := postForm(ctx, c, endpoint, form, &tr); err != nil {
		return nil, err
	}
	if tr.AccessToken == "" {
		return nil, errors.New("the token endpoint returned no access token")
	}
	return &tr, nil
}

// refreshOAuth renews an OAuth login with its refresh token (rotated) and saves it.
func refreshOAuth(ctx context.Context, c *Client, creds *Credentials, cr *Credential) error {
	tr, err := tokenRequest(ctx, c, cr.TokenEndpoint, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {cr.RefreshToken}, "client_id": {OAuthClientId},
	})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	cr.Token, cr.Expires = tr.AccessToken, time.Time{}
	if tr.RefreshToken != "" {
		cr.RefreshToken = tr.RefreshToken
	}
	if tr.ExpiresIn > 0 {
		cr.Expires = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return creds.Save()
}

// revokeOAuth revokes an OAuth login's refresh token (and with it the access token) on the remote.
func revokeOAuth(ctx context.Context, c *Client, cr *Credential) error {
	if cr.RevokeEndpoint == "" || cr.RefreshToken == "" {
		return nil
	}
	return postForm(ctx, c, cr.RevokeEndpoint, url.Values{"token": {cr.RefreshToken}, "client_id": {OAuthClientId}}, nil)
}
