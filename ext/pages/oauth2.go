package pages

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/auth"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"golang.org/x/oauth2"
)

// OAuth2 for the auth built-in. requestOAuth2 builds the provider URL here (as PocketBase's auth-methods API does)
// and keeps the state in a short-lived cookie. signInWithOAuth2 checks the state, then completes the sign-in through
// PocketBase's own auth-with-oauth2 endpoint, in-process, so account linking, sign-up, the pre-hijacking checks and
// OnRecordAuthWithOAuth2Request hooks are PocketBase's.

const oauth2Cookie = "pb_oauth2"

// oauth2StateTTL is how long a started sign-in can be completed.
const oauth2StateTTL = 10 * time.Minute

type oauth2State struct {
	State        string `json:"state"`
	Provider     string `json:"provider"`
	Collection   string `json:"collection"`
	CodeVerifier string `json:"codeVerifier"`
	RedirectURL  string `json:"redirectURL"`
}

func (r *request) origin() string {
	scheme := "http"
	if r.secure() {
		scheme = "https"
	}
	return scheme + "://" + r.e.Request.Host
}

// oauth2CookieOpts: Apple answers with a cross-site POST (form_post), which a SameSite=Lax cookie doesn't survive, so
// on HTTPS the state cookie is SameSite=None. It holds nothing that signs anyone in without the provider's code.
func (r *request) oauth2CookieOpts(maxAge int) map[string]any {
	o := map[string]any{"maxAge": maxAge}
	if r.secure() {
		o["sameSite"] = "none"
	}
	return o
}

func (r *request) requestOAuth2(providerName string, opts map[string]any) string {
	col := r.authCollection(opts)
	if !col.OAuth2.Enabled {
		r.fail400("OAuth2 sign-in is turned off for " + col.Name)
	}
	cfg, ok := col.OAuth2.GetProviderConfig(providerName)
	if !ok {
		r.fail400(fmt.Sprintf("OAuth2 provider %q isn't set up for %s", providerName, col.Name))
	}
	provider, err := cfg.InitProvider()
	if err != nil {
		panic(r.vm.NewGoError(fmt.Errorf("OAuth2 provider %q: %w", providerName, err)))
	}

	redirectPath := "/auth/oauth/confirm"
	if v, ok := opts["redirectPath"].(string); ok && v != "" {
		redirectPath = v
	}
	if u, err := url.Parse(redirectPath); err != nil || u.IsAbs() || u.Host != "" || len(redirectPath) == 0 || redirectPath[0] != '/' {
		r.fail400("ctx.session.requestOAuth2: redirectPath must be a path on this site, like /auth/oauth/confirm")
	}

	st := oauth2State{
		State:       security.RandomString(30),
		Provider:    providerName,
		Collection:  col.Name,
		RedirectURL: r.origin() + redirectPath,
	}
	provider.SetRedirectURL(st.RedirectURL)

	var urlOpts []oauth2.AuthCodeOption
	if providerName == auth.NameApple {
		urlOpts = append(urlOpts, oauth2.SetAuthURLParam("response_mode", "form_post"))
	}
	if provider.PKCE() {
		st.CodeVerifier = security.RandomString(43)
		urlOpts = append(urlOpts,
			oauth2.SetAuthURLParam("code_challenge", security.S256Challenge(st.CodeVerifier)),
			oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		)
	}
	authURL := provider.BuildAuthURL(st.State, urlOpts...)

	val, _ := json.Marshal(st)
	r.setCookie(oauth2Cookie, string(val), r.oauth2CookieOpts(int(oauth2StateTTL/time.Second)))

	if v, ok := opts["redirect"].(bool); !ok || v {
		if r.sent {
			panic(r.vm.NewGoError(fmt.Errorf("a response was already sent")))
		}
		r.sent = true
		http.Redirect(r.e.Response, r.e.Request, authURL, http.StatusFound)
	}
	return authURL
}

func (r *request) signInWithOAuth2(state, code string, opts map[string]any) map[string]any {
	raw := r.cookies()[oauth2Cookie]
	var st oauth2State
	if raw == "" || json.Unmarshal([]byte(raw), &st) != nil || st.State == "" {
		r.fail400("This sign-in link expired or was already used. Please try again.")
	}
	r.setCookie(oauth2Cookie, "", r.oauth2CookieOpts(0)) // one use
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(st.State)) != 1 {
		r.fail400("This sign-in link expired or was already used. Please try again.")
	}
	if code == "" {
		r.fail400("The sign-in provider didn't send a code. Please try again.")
	}

	body := map[string]any{
		"provider":     st.Provider,
		"code":         code,
		"codeVerifier": st.CodeVerifier,
		"redirectURL":  st.RedirectURL,
	}
	if data, ok := opts["createData"].(map[string]any); ok {
		body["createData"] = data
	}
	headers := map[string]any{"Content-Type": "application/json"}
	if r.auth != nil && r.auth.Collection().Name == st.Collection {
		headers["Authorization"] = r.authToken() // link the provider to the signed-in account
	}
	res := r.apiSend(map[string]any{
		"url":     sdkBaseURL + "/api/collections/" + url.PathEscape(st.Collection) + "/auth-with-oauth2",
		"method":  http.MethodPost,
		"body":    body,
		"headers": headers,
	})
	status, _ := res["statusCode"].(int)
	parsed, _ := res["json"].(map[string]any)
	if status != http.StatusOK {
		msg, _ := parsed["message"].(string)
		if msg == "" {
			msg = "Failed to authenticate."
		}
		if _, mfa := parsed["mfaId"]; mfa {
			msg = "This account needs a second sign-in step (MFA), which pages doesn't support yet."
		}
		r.fail400(msg)
	}
	token, _ := parsed["token"].(string)
	rec, err := r.p.app.FindAuthRecordByToken(token, core.TokenTypeAuth)
	if err != nil {
		panic(r.vm.NewGoError(fmt.Errorf("OAuth2: %w", err)))
	}
	out := r.signIn(rec)
	out["meta"] = parsed["meta"]
	return out
}
