package oauthserver

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/mails"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"github.com/pockethost/pocketbase/ext/tokens"
)

const (
	sessionCookie = "pb_oauth"
	csrfCookie    = "pb_oauth_csrf"
	sessionTTL    = 15 * time.Minute
	codeTTL       = 60 * time.Second
	deviceTTL     = 10 * time.Minute

	authorizePath    = "/oauth/authorize"
	tokenPath        = "/oauth/token"
	devicePath       = "/oauth/device"
	deviceVerifyPath = "/oauth/device/verify"
	revokePath       = "/oauth/revoke"
	registerPath     = "/oauth/register"
)

// pollInterval is the device flow's polling interval in seconds (tests shorten it).
var pollInterval = 5

func (s *Server) bindRoutes(se *core.ServeEvent) {
	se.Router.GET(MetadataPath, s.metadata)
	se.Router.GET(authorizePath, s.authorize)
	se.Router.POST(authorizePath, s.authorize)
	se.Router.POST(tokenPath, s.token)
	se.Router.POST(devicePath, s.deviceAuthorization)
	se.Router.GET(deviceVerifyPath, s.deviceVerify)
	se.Router.POST(deviceVerifyPath, s.deviceVerify)
	se.Router.POST(revokePath, s.revoke)
	se.Router.POST(registerPath, s.register) // answers 404 unless dynamic registration is on
}

// issuer is the public base URL: Config.Issuer, else the request's own scheme and host (so it always matches the
// URL the metadata was fetched from).
func (s *Server) issuer(e *core.RequestEvent) string {
	if s.cfg.Issuer != "" {
		return strings.TrimRight(s.cfg.Issuer, "/")
	}
	scheme := "http"
	if e.IsTLS() || strings.EqualFold(e.Request.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + e.Request.Host
}

func (s *Server) metadata(e *core.RequestEvent) error {
	iss := s.issuer(e)
	scopes := []string{OfflineAccess}
	for _, sc := range s.tokens().Scopes() {
		scopes = append(scopes, sc.Name)
	}
	m := map[string]any{
		"issuer":                                         iss,
		"authorization_endpoint":                         iss + authorizePath,
		"token_endpoint":                                 iss + tokenPath,
		"device_authorization_endpoint":                  iss + devicePath,
		"revocation_endpoint":                            iss + revokePath,
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:device_code"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none", "client_secret_post", "client_secret_basic"},
		"scopes_supported":                               scopes,
		"client_id_metadata_document_supported":          len(s.cfg.CIMDHosts) > 0,
		"authorization_response_iss_parameter_supported": true,
	}
	if s.dcr.Load() {
		m["registration_endpoint"] = iss + registerPath
	}
	return e.JSON(http.StatusOK, m)
}

// ---------------------------------------------------------------------------------------------------------------
// authorize

// authRequest is an /oauth/authorize request (or the device verification it stands in for).
type authRequest struct {
	client      *Client
	redirectURI string
	state       string
	challenge   string
	scope       string
	resource    string       // RFC 8707 audience, "" for none
	device      *core.Record // set on the device verification page
}

// validResource: an absolute http(s) URL on this server's host, without a fragment (RFC 8707).
func (s *Server) validResource(e *core.RequestEvent, r string) bool {
	u, err := url.Parse(r)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Fragment != "" {
		return false
	}
	iss, _ := url.Parse(s.issuer(e))
	return strings.EqualFold(u.Host, e.Request.Host) || (iss != nil && strings.EqualFold(u.Host, iss.Host))
}

func param(e *core.RequestEvent, name string) string {
	if e.Request.Method == http.MethodPost {
		return e.Request.PostFormValue(name)
	}
	return e.Request.URL.Query().Get(name)
}

// parseAuthorize validates the request. A problem with the client or redirect URI is shown as a page (redirect=false);
// anything else can be sent back to the client.
func (s *Server) parseAuthorize(e *core.RequestEvent) (*authRequest, string, bool) {
	c, err := s.FindClient(param(e, "client_id"))
	if err != nil {
		return nil, "Unknown client.", false
	}
	ar := &authRequest{client: c, redirectURI: param(e, "redirect_uri"), state: param(e, "state"),
		challenge: param(e, "code_challenge"), scope: param(e, "scope"), resource: param(e, "resource")}
	if ar.redirectURI == "" && len(c.RedirectURIs) == 1 {
		ar.redirectURI = c.RedirectURIs[0]
	}
	if !c.AllowsRedirect(ar.redirectURI) {
		return nil, "This client didn't register that redirect address.", false
	}
	if ar.resource != "" && !s.validResource(e, ar.resource) {
		return ar, "invalid_target: resource must be a URL on this server", true
	}
	if param(e, "response_type") != "code" {
		return ar, "unsupported_response_type", true
	}
	if ar.challenge == "" || param(e, "code_challenge_method") != "S256" {
		return ar, "invalid_request: PKCE with code_challenge_method=S256 is required", true
	}
	return ar, "", true
}

func (ar *authRequest) hidden() map[string]string {
	if ar.device != nil {
		return map[string]string{"user_code": ar.device.GetString("user_code")}
	}
	return map[string]string{
		"client_id": ar.client.Id, "redirect_uri": ar.redirectURI, "state": ar.state, "scope": ar.scope,
		"response_type": "code", "code_challenge": ar.challenge, "code_challenge_method": "S256",
		"resource": ar.resource,
	}
}

func (ar *authRequest) errorRedirect(e *core.RequestEvent, code string) error {
	desc := ""
	if c, d, ok := strings.Cut(code, ": "); ok {
		code, desc = c, d
	}
	u, _ := url.Parse(ar.redirectURI)
	q := u.Query()
	q.Set("error", code)
	if desc != "" {
		q.Set("error_description", desc)
	}
	if ar.state != "" {
		q.Set("state", ar.state)
	}
	u.RawQuery = q.Encode()
	return e.Redirect(http.StatusFound, u.String())
}

func (s *Server) authorize(e *core.RequestEvent) error {
	if e.Request.Method == http.MethodPost {
		_ = e.Request.ParseForm()
	}
	ar, problem, canRedirect := s.parseAuthorize(e)
	if !canRedirect {
		return s.render(e, http.StatusBadRequest, PageData{Page: PageError, Error: problem})
	}
	if problem != "" {
		return ar.errorRedirect(e, problem)
	}
	return s.flow(e, ar, authorizePath)
}

// flow runs sign-in → consent for an authorize request or a device verification.
func (s *Server) flow(e *core.RequestEvent, ar *authRequest, action string) error {
	base := s.basePage(e, ar, action)
	if _, err := s.resolveScopes(ar.scope, nil); err != nil {
		if ar.device == nil {
			return ar.errorRedirect(e, "invalid_scope: "+err.Error())
		}
		base.Page, base.Error = PageDone, err.Error()
		return s.render(e, http.StatusBadRequest, base)
	}
	user := s.sessionUser(e)

	if e.Request.Method == http.MethodPost {
		if !s.checkCSRF(e) {
			base.Page, base.Error = PageError, "This form expired. Go back and try again."
			return s.render(e, http.StatusForbidden, base)
		}
		switch e.Request.PostFormValue("step") {
		case "password":
			return s.stepPassword(e, ar, base)
		case "request_otp":
			return s.stepRequestOTP(e, ar, base, "")
		case "otp":
			return s.stepOTP(e, ar, base)
		case "switch":
			s.clearSession(e)
			return s.signInPage(e, base, "", "")
		case "allow", "deny":
			if user == nil {
				return s.signInPage(e, base, "Your sign-in expired. Sign in again.", "")
			}
			return s.decide(e, ar, base, user, e.Request.PostFormValue("step") == "allow")
		}
	}
	if user == nil {
		return s.signInPage(e, base, "", "")
	}
	return s.consentPage(e, ar, base, user)
}

func (s *Server) basePage(e *core.RequestEvent, ar *authRequest, action string) PageData {
	d := PageData{Action: action, Hidden: ar.hidden(), CSRF: s.csrf(e), ClientName: ar.client.Name}
	if ar.device == nil {
		if u, err := url.Parse(ar.redirectURI); err == nil {
			d.ClientHost = u.Host
			if isLoopback(u.Hostname()) {
				d.ClientHost = "an app on this computer"
			}
		}
	}
	return d
}

func (s *Server) signInPage(e *core.RequestEvent, d PageData, errMsg, email string) error {
	d.Page, d.Error, d.Email = PageSignIn, errMsg, email
	d.Collections = s.cfg.Collections
	d.Collection = e.Request.PostFormValue("collection")
	if !containsStr(d.Collections, d.Collection) {
		d.Collection = d.Collections[0]
	}
	if col, err := s.app.FindCachedCollectionByNameOrId(d.Collection); err == nil {
		d.PasswordEnabled = col.PasswordAuth.Enabled
		d.OTPEnabled = col.OTP.Enabled
	}
	status := http.StatusOK
	if errMsg != "" {
		status = http.StatusUnauthorized
	}
	return s.render(e, status, d)
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Server) signInCollection(e *core.RequestEvent) (*core.Collection, error) {
	name := e.Request.PostFormValue("collection")
	if name == "" {
		name = s.cfg.Collections[0]
	}
	if !containsStr(s.cfg.Collections, name) {
		return nil, errors.New("This account type can't sign in here.")
	}
	col, err := s.app.FindCachedCollectionByNameOrId(name)
	if err != nil || !col.IsAuth() {
		return nil, errors.New("This account type can't sign in here.")
	}
	return col, nil
}

const badCredentials = "Wrong email or password."

func (s *Server) stepPassword(e *core.RequestEvent, ar *authRequest, d PageData) error {
	email := strings.TrimSpace(e.Request.PostFormValue("email"))
	ip := e.RealIP()
	if s.limiter.blocked(ip) {
		return s.signInPage(e, d, "Too many attempts. Wait a few minutes and try again.", email)
	}
	col, err := s.signInCollection(e)
	if err != nil {
		return s.signInPage(e, d, err.Error(), email)
	}
	if !col.PasswordAuth.Enabled {
		return s.signInPage(e, d, "Password sign-in is off for this account type.", email)
	}
	rec, err := s.app.FindAuthRecordByEmail(col, email)
	if err != nil || !rec.ValidatePassword(e.Request.PostFormValue("password")) {
		s.limiter.fail(ip)
		return s.signInPage(e, d, badCredentials, email)
	}
	if col.MFA.Enabled {
		// second factor: a code by email, bound to this password check
		return s.stepRequestOTP(e, ar, d, s.sign("mfa", rec.Id))
	}
	return s.signedIn(e, ar, d, rec)
}

func (s *Server) stepRequestOTP(e *core.RequestEvent, ar *authRequest, d PageData, mfa string) error {
	email := strings.TrimSpace(e.Request.PostFormValue("email"))
	col, err := s.signInCollection(e)
	if err != nil {
		return s.signInPage(e, d, err.Error(), email)
	}
	if mfa == "" && !col.OTP.Enabled {
		return s.signInPage(e, d, "Sign-in codes are off for this account type.", email)
	}
	if s.limiter.blocked(e.RealIP()) {
		return s.signInPage(e, d, "Too many attempts. Wait a few minutes and try again.", email)
	}
	otpId := ""
	if rec, err := s.app.FindAuthRecordByEmail(col, email); err == nil {
		pass := security.RandomStringWithAlphabet(max(col.OTP.Length, 6), "0123456789")
		otp := core.NewOTP(s.app)
		otp.SetCollectionRef(col.Id)
		otp.SetRecordRef(rec.Id)
		otp.SetPassword(pass)
		otp.SetSentTo(rec.Email())
		if err := s.app.Save(otp); err != nil {
			return err
		}
		if err := mails.SendRecordOTP(s.app, rec, otp.Id, pass); err != nil {
			s.app.Logger().Error("oauth-server: send OTP", "error", err)
		}
		otpId = otp.Id
	} else {
		otpId = security.RandomString(15) // don't reveal whether the account exists
	}
	d.Page, d.Email, d.Collection, d.OTPId = PageOTP, email, col.Name, otpId
	if mfa != "" {
		d.Hidden = cloneMap(d.Hidden)
		d.Hidden["mfa"] = mfa
	}
	return s.render(e, http.StatusOK, d)
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Server) stepOTP(e *core.RequestEvent, ar *authRequest, d PageData) error {
	email := strings.TrimSpace(e.Request.PostFormValue("email"))
	ip := e.RealIP()
	col, err := s.signInCollection(e)
	if err != nil {
		return s.signInPage(e, d, err.Error(), email)
	}
	retry := func(msg string) error {
		s.limiter.fail(ip)
		d.Page, d.Email, d.Collection, d.OTPId, d.Error = PageOTP, email, col.Name, e.Request.PostFormValue("otp_id"), msg
		if mfa := e.Request.PostFormValue("mfa"); mfa != "" {
			d.Hidden = cloneMap(d.Hidden)
			d.Hidden["mfa"] = mfa
		}
		return s.render(e, http.StatusUnauthorized, d)
	}
	if s.limiter.blocked(ip) {
		return s.signInPage(e, d, "Too many attempts. Wait a few minutes and try again.", email)
	}
	otp, err := s.app.FindOTPById(e.Request.PostFormValue("otp_id"))
	if err != nil || otp.CollectionRef() != col.Id || otp.HasExpired(col.OTP.DurationTime()) ||
		!otp.ValidatePassword(strings.TrimSpace(e.Request.PostFormValue("code"))) {
		return retry("That code is wrong or expired.")
	}
	rec, err := s.app.FindRecordById(col, otp.RecordRef())
	if err != nil || !strings.EqualFold(rec.Email(), email) {
		return retry("That code is wrong or expired.")
	}
	mfa := e.Request.PostFormValue("mfa")
	if col.MFA.Enabled && subtle.ConstantTimeCompare([]byte(mfa), []byte(s.sign("mfa", rec.Id))) != 1 {
		return s.signInPage(e, d, "This account needs its password and a code. Sign in with your password first.", email)
	}
	_ = s.app.Delete(otp)
	return s.signedIn(e, ar, d, rec)
}

func (s *Server) signedIn(e *core.RequestEvent, ar *authRequest, d PageData, rec *core.Record) error {
	tok, err := rec.NewStaticAuthToken(sessionTTL)
	if err != nil {
		return err
	}
	e.SetCookie(&http.Cookie{Name: sessionCookie, Value: tok, Path: "/oauth", HttpOnly: true, Secure: e.IsTLS(),
		SameSite: http.SameSiteLaxMode, MaxAge: int(sessionTTL.Seconds())})
	return s.consentPage(e, ar, d, rec)
}

func (s *Server) sessionUser(e *core.RequestEvent) *core.Record {
	c, err := e.Request.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	rec, err := s.app.FindAuthRecordByToken(c.Value, core.TokenTypeAuth)
	if err != nil || !s.allowedCollection(rec.Collection()) {
		return nil
	}
	return rec
}

func (s *Server) clearSession(e *core.RequestEvent) {
	e.SetCookie(&http.Cookie{Name: sessionCookie, Value: "", Path: "/oauth", HttpOnly: true, MaxAge: -1})
}

func (s *Server) csrf(e *core.RequestEvent) string {
	if c, err := e.Request.Cookie(csrfCookie); err == nil && len(c.Value) >= 20 {
		return c.Value
	}
	v := security.RandomString(32)
	e.SetCookie(&http.Cookie{Name: csrfCookie, Value: v, Path: "/oauth", HttpOnly: true, Secure: e.IsTLS(), SameSite: http.SameSiteLaxMode})
	// the same request may render a form right away
	e.Request.AddCookie(&http.Cookie{Name: csrfCookie, Value: v})
	return v
}

func (s *Server) checkCSRF(e *core.RequestEvent) bool {
	c, err := e.Request.Cookie(csrfCookie)
	return err == nil && c.Value != "" && subtle.ConstantTimeCompare([]byte(c.Value), []byte(e.Request.PostFormValue("csrf"))) == 1
}

func (s *Server) consentPage(e *core.RequestEvent, ar *authRequest, d PageData, user *core.Record) error {
	scopes, err := s.resolveScopes(ar.scope, user.Collection())
	if err != nil {
		if ar.device != nil {
			d.Page, d.Error = PageDone, err.Error()
			return s.render(e, http.StatusForbidden, d)
		}
		return ar.errorRedirect(e, "invalid_scope: "+err.Error())
	}
	if !s.tokens().AllowedCollection(user.Collection()) {
		d.Page, d.Error = PageError, user.Collection().Name+" accounts can't hold API tokens on this server."
		return s.render(e, http.StatusForbidden, d)
	}
	d.Page, d.Email, d.Scopes, d.FullAccess = PageConsent, user.Email(), scopes, len(scopes) == 0
	return s.render(e, http.StatusOK, d)
}

func (s *Server) decide(e *core.RequestEvent, ar *authRequest, d PageData, user *core.Record, allow bool) error {
	scopes, err := s.resolveScopes(ar.scope, user.Collection())
	if err != nil {
		return s.consentPage(e, ar, d, user)
	}
	if ar.device != nil {
		dev := ar.device
		if allow {
			dev.Set("status", StatusApproved)
			dev.Set("owner_collection", user.Collection().Id)
			dev.Set("owner", user.Id)
		} else {
			dev.Set("status", StatusDenied)
		}
		if err := s.app.Save(dev); err != nil {
			return err
		}
		d.Page = PageDone
		if allow {
			d.Message = ar.client.Name + " is connected. You can close this page and go back to your device."
		} else {
			d.Error = "You denied access. You can close this page."
		}
		return s.render(e, http.StatusOK, d)
	}
	if !allow {
		return ar.errorRedirect(e, "access_denied")
	}
	code := security.RandomString(40)
	col, err := s.app.FindCachedCollectionByNameOrId(CodesCollectionId)
	if err != nil {
		return err
	}
	rec := core.NewRecord(col)
	rec.Set("kind", KindCode)
	rec.Set("hash", hashSecret(code))
	rec.Set("client_id", ar.client.Id)
	rec.Set("redirect_uri", ar.redirectURI)
	rec.Set("challenge", ar.challenge)
	rec.Set("resource", ar.resource)
	rec.Set("scopes", scopeNames(scopes))
	rec.Set("owner_collection", user.Collection().Id)
	rec.Set("owner", user.Id)
	rec.Set("expires", s.cfg.Now().Add(codeTTL).UTC())
	if err := s.app.Save(rec); err != nil {
		return err
	}
	u, _ := url.Parse(ar.redirectURI)
	q := u.Query()
	q.Set("code", code)
	if ar.state != "" {
		q.Set("state", ar.state)
	}
	q.Set("iss", s.issuer(e))
	u.RawQuery = q.Encode()
	return e.Redirect(http.StatusFound, u.String())
}

// ---------------------------------------------------------------------------------------------------------------
// device flow (RFC 8628)

const userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ" // no vowels: no words

func normalizeUserCode(v string) string {
	v = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(v)))
	if len(v) == 8 {
		return v[:4] + "-" + v[4:]
	}
	return v
}

func (s *Server) deviceAuthorization(e *core.RequestEvent) error {
	_ = e.Request.ParseForm()
	c, err := s.clientAuth(e)
	if err != nil {
		return oauthError(e, http.StatusUnauthorized, "invalid_client", err.Error())
	}
	scope := e.Request.PostFormValue("scope")
	if _, err := s.resolveScopes(scope, nil); err != nil {
		return oauthError(e, http.StatusBadRequest, "invalid_scope", err.Error())
	}
	resource := e.Request.PostFormValue("resource")
	if resource != "" && !s.validResource(e, resource) {
		return oauthError(e, http.StatusBadRequest, "invalid_target", "resource must be a URL on this server")
	}
	deviceCode := security.RandomString(40)
	userCode := normalizeUserCode(security.RandomStringWithAlphabet(8, userCodeAlphabet))
	col, err := s.app.FindCachedCollectionByNameOrId(CodesCollectionId)
	if err != nil {
		return err
	}
	rec := core.NewRecord(col)
	rec.Set("kind", KindDevice)
	rec.Set("hash", hashSecret(deviceCode))
	rec.Set("user_code", userCode)
	rec.Set("client_id", c.Id)
	rec.Set("scopes", strings.Fields(scope))
	rec.Set("resource", resource)
	rec.Set("status", StatusPending)
	rec.Set("expires", s.cfg.Now().Add(deviceTTL).UTC())
	if err := s.app.Save(rec); err != nil {
		return err
	}
	verify := s.issuer(e) + deviceVerifyPath
	e.Response.Header().Set("Cache-Control", "no-store")
	return e.JSON(http.StatusOK, map[string]any{
		"device_code": deviceCode, "user_code": userCode, "verification_uri": verify,
		"verification_uri_complete": verify + "?user_code=" + url.QueryEscape(userCode),
		"expires_in":                int(deviceTTL.Seconds()), "interval": pollInterval,
	})
}

func (s *Server) deviceVerify(e *core.RequestEvent) error {
	if e.Request.Method == http.MethodPost {
		_ = e.Request.ParseForm()
	}
	userCode := normalizeUserCode(param(e, "user_code"))
	base := PageData{Page: PageDevice, Action: deviceVerifyPath, UserCode: userCode, CSRF: s.csrf(e)}
	if userCode == "" {
		return s.render(e, http.StatusOK, base)
	}
	dev, err := s.app.FindFirstRecordByData(CodesCollectionId, "user_code", userCode)
	if err != nil || dev.GetString("kind") != KindDevice || dev.GetString("status") != StatusPending ||
		!dev.GetDateTime("expires").Time().After(s.cfg.Now()) {
		base.Error = "That code is wrong or expired. Check your device for a new one."
		return s.render(e, http.StatusNotFound, base)
	}
	c, err := s.FindClient(dev.GetString("client_id"))
	if err != nil {
		base.Error = "That code is wrong or expired."
		return s.render(e, http.StatusNotFound, base)
	}
	var scopes []string
	_ = dev.UnmarshalJSONField("scopes", &scopes)
	ar := &authRequest{client: c, scope: strings.Join(scopes, " "), device: dev}
	return s.flow(e, ar, deviceVerifyPath)
}

// ---------------------------------------------------------------------------------------------------------------
// token endpoint

func oauthError(e *core.RequestEvent, status int, code, desc string) error {
	e.Response.Header().Set("Cache-Control", "no-store")
	body := map[string]string{"error": code}
	if desc != "" {
		body["error_description"] = desc
	}
	return e.JSON(status, body)
}

// clientAuth identifies the client: client_id (+ client_secret) in the form, or HTTP Basic.
func (s *Server) clientAuth(e *core.RequestEvent) (*Client, error) {
	id, secret, ok := e.Request.BasicAuth()
	if ok {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = e.Request.PostFormValue("client_id"), e.Request.PostFormValue("client_secret")
	}
	c, err := s.FindClient(id)
	if err != nil {
		return nil, err
	}
	if !c.checkSecret(secret) {
		return nil, errors.New("client authentication failed")
	}
	return c, nil
}

func (s *Server) token(e *core.RequestEvent) error {
	_ = e.Request.ParseForm()
	c, err := s.clientAuth(e)
	if err != nil {
		return oauthError(e, http.StatusUnauthorized, "invalid_client", err.Error())
	}
	switch e.Request.PostFormValue("grant_type") {
	case "authorization_code":
		return s.grantCode(e, c)
	case "refresh_token":
		return s.grantRefresh(e, c)
	case "urn:ietf:params:oauth:grant-type:device_code":
		return s.grantDevice(e, c)
	}
	return oauthError(e, http.StatusBadRequest, "unsupported_grant_type", "")
}

// takeCode finds a code row by its secret; single-use kinds are deleted on the spot.
func (s *Server) findCode(kind, secret string) (*core.Record, error) {
	if secret == "" {
		return nil, sql.ErrNoRows
	}
	rec, err := s.app.FindFirstRecordByData(CodesCollectionId, "hash", hashSecret(secret))
	if err != nil {
		return nil, err
	}
	if rec.GetString("kind") != kind {
		return nil, sql.ErrNoRows
	}
	return rec, nil
}

func (s *Server) grantCode(e *core.RequestEvent, c *Client) error {
	rec, err := s.findCode(KindCode, e.Request.PostFormValue("code"))
	if err != nil {
		return oauthError(e, http.StatusBadRequest, "invalid_grant", "unknown or used code")
	}
	_ = s.app.Delete(rec) // single use, whatever happens next
	if !rec.GetDateTime("expires").Time().After(s.cfg.Now()) {
		return oauthError(e, http.StatusBadRequest, "invalid_grant", "the code expired")
	}
	if rec.GetString("client_id") != c.Id || rec.GetString("redirect_uri") != e.Request.PostFormValue("redirect_uri") {
		return oauthError(e, http.StatusBadRequest, "invalid_grant", "client or redirect_uri mismatch")
	}
	sum := sha256.Sum256([]byte(e.Request.PostFormValue("code_verifier")))
	if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(rec.GetString("challenge"))) != 1 {
		return oauthError(e, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
	}
	return s.issue(e, c, rec)
}

func (s *Server) grantRefresh(e *core.RequestEvent, c *Client) error {
	rec, err := s.findCode(KindRefresh, e.Request.PostFormValue("refresh_token"))
	if err != nil || rec.GetString("client_id") != c.Id {
		return oauthError(e, http.StatusBadRequest, "invalid_grant", "unknown refresh token")
	}
	_ = s.app.Delete(rec)
	if old := rec.GetString("token_id"); old != "" {
		_ = s.tokens().Revoke(old)
	}
	if !rec.GetDateTime("expires").Time().After(s.cfg.Now()) {
		return oauthError(e, http.StatusBadRequest, "invalid_grant", "the refresh token expired")
	}
	return s.issue(e, c, rec)
}

func (s *Server) grantDevice(e *core.RequestEvent, c *Client) error {
	rec, err := s.findCode(KindDevice, e.Request.PostFormValue("device_code"))
	if err != nil || rec.GetString("client_id") != c.Id {
		return oauthError(e, http.StatusBadRequest, "invalid_grant", "unknown device code")
	}
	now := s.cfg.Now()
	if !rec.GetDateTime("expires").Time().After(now) {
		_ = s.app.Delete(rec)
		return oauthError(e, http.StatusBadRequest, "expired_token", "")
	}
	switch rec.GetString("status") {
	case StatusDenied:
		_ = s.app.Delete(rec)
		return oauthError(e, http.StatusBadRequest, "access_denied", "")
	case StatusApproved:
		_ = s.app.Delete(rec)
		return s.issue(e, c, rec)
	}
	last := rec.GetDateTime("last_poll")
	rec.Set("last_poll", now.UTC())
	_ = s.app.Save(rec)
	if !last.IsZero() && now.Sub(last.Time()) < time.Duration(pollInterval)*time.Second*4/5 {
		return oauthError(e, http.StatusBadRequest, "slow_down", "")
	}
	return oauthError(e, http.StatusBadRequest, "authorization_pending", "")
}

// issue mints an access token (an API token) and a refresh token for the owner and scopes of rec.
func (s *Server) issue(e *core.RequestEvent, c *Client, rec *core.Record) error {
	col, err := s.app.FindCachedCollectionByNameOrId(rec.GetString("owner_collection"))
	if err != nil || !s.allowedCollection(col) {
		return oauthError(e, http.StatusBadRequest, "invalid_grant", "the account can't sign in here")
	}
	owner, err := s.app.FindRecordById(col, rec.GetString("owner"))
	if err != nil {
		return oauthError(e, http.StatusBadRequest, "invalid_grant", "the account is gone")
	}
	var scopes []string
	_ = rec.UnmarshalJSONField("scopes", &scopes)
	scopes = slices.DeleteFunc(scopes, func(v string) bool { return v == OfflineAccess })
	audience := rec.GetString("resource")
	if r := e.Request.PostFormValue("resource"); r != "" {
		switch {
		case audience != "" && r != audience:
			return oauthError(e, http.StatusBadRequest, "invalid_target", "resource differs from the authorized one")
		case audience == "" && !s.validResource(e, r):
			return oauthError(e, http.StatusBadRequest, "invalid_target", "resource must be a URL on this server")
		}
		audience = r
	}
	now := s.cfg.Now()
	access, tokRec, err := s.tokens().Create(owner, tokens.CreateOptions{
		Name: c.Name + " (OAuth)", Scopes: scopes, Expires: now.Add(s.cfg.AccessTTL), ClientId: c.Id, Audience: audience,
	})
	if err != nil {
		return oauthError(e, http.StatusBadRequest, "invalid_scope", err.Error())
	}
	refresh := "pbr_" + security.RandomString(40)
	codes, err := s.app.FindCachedCollectionByNameOrId(CodesCollectionId)
	if err != nil {
		return err
	}
	r := core.NewRecord(codes)
	r.Set("kind", KindRefresh)
	r.Set("hash", hashSecret(refresh))
	r.Set("client_id", c.Id)
	r.Set("scopes", scopes)
	r.Set("owner_collection", col.Id)
	r.Set("owner", owner.Id)
	r.Set("token_id", tokRec.Id)
	r.Set("resource", audience)
	r.Set("expires", now.Add(s.cfg.RefreshTTL).UTC())
	if err := s.app.Save(r); err != nil {
		return err
	}
	e.Response.Header().Set("Cache-Control", "no-store")
	exp := tokRec.GetDateTime("expires").Time().Sub(now)
	return e.JSON(http.StatusOK, map[string]any{
		"access_token": access, "token_type": "Bearer", "expires_in": int(exp.Seconds()),
		"refresh_token": refresh, "scope": strings.Join(scopes, " "),
	})
}

// ---------------------------------------------------------------------------------------------------------------
// revoke (RFC 7009) and registration (RFC 7591)

func (s *Server) revoke(e *core.RequestEvent) error {
	_ = e.Request.ParseForm()
	c, err := s.clientAuth(e)
	if err != nil {
		return oauthError(e, http.StatusUnauthorized, "invalid_client", err.Error())
	}
	tok := e.Request.PostFormValue("token")
	switch {
	case strings.HasPrefix(tok, tokens.Prefix):
		if info, err := s.tokens().Verify(tok); err == nil && info.Token.GetString("client_id") == c.Id {
			_ = s.tokens().Revoke(info.Token.Id)
			if r, err := s.app.FindFirstRecordByData(CodesCollectionId, "token_id", info.Token.Id); err == nil {
				_ = s.app.Delete(r)
			}
		}
	default:
		if r, err := s.findCode(KindRefresh, tok); err == nil && r.GetString("client_id") == c.Id {
			if id := r.GetString("token_id"); id != "" {
				_ = s.tokens().Revoke(id)
			}
			_ = s.app.Delete(r)
		}
	}
	return e.NoContent(http.StatusOK)
}

func (s *Server) register(e *core.RequestEvent) error {
	if !s.dcr.Load() {
		return e.NotFoundError("", nil)
	}
	var body struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if err := e.BindBody(&body); err != nil {
		return oauthError(e, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON")
	}
	public := body.AuthMethod == "" || body.AuthMethod == "none"
	if !public && body.AuthMethod != "client_secret_post" && body.AuthMethod != "client_secret_basic" {
		return oauthError(e, http.StatusBadRequest, "invalid_client_metadata", "unsupported token_endpoint_auth_method")
	}
	c, secret, err := s.AddClient(body.ClientName, body.RedirectURIs, public, true)
	if err != nil {
		return oauthError(e, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
	}
	method := "none"
	if !public {
		method = body.AuthMethod
	}
	out := map[string]any{
		"client_id": c.Id, "client_name": c.Name, "redirect_uris": c.RedirectURIs, "token_endpoint_auth_method": method,
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"client_id_issued_at": s.cfg.Now().Unix(),
	}
	if secret != "" {
		out["client_secret"] = secret
		out["client_secret_expires_at"] = 0
	}
	return e.JSON(http.StatusCreated, out)
}
