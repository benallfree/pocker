package remotes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ImpersonateTTL is the lifetime of a superuser login on a remote without the tokens plugin.
var ImpersonateTTL = 90 * 24 * time.Hour

// LoginResult is a finished sign-in, ready to save.
type LoginResult struct {
	Credential *Credential
	// Warning, if set, is shown to the user (e.g. an impersonate login can't be revoked on its own).
	Warning string
	// Minted: the CLI created an API token on the remote (logout revokes it).
	Minted  bool
	TokenId string
}

// LoginWithToken checks a pasted token (pbt_..., a PocketHost token, or a JWT) with the remote's whoami when it has
// one; a remote without whoami (a PocketHost edge today) takes it as is.
func LoginWithToken(ctx context.Context, c *Client, token string) (*LoginResult, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("empty token")
	}
	probe := *c
	probe.Token = token
	cred := &Credential{Token: token, Via: "token", Created: time.Now().UTC()}
	w, err := probe.Whoami(ctx)
	switch {
	case errors.Is(err, ErrNoWhoami):
		return &LoginResult{Credential: cred}, nil
	case err != nil:
		return nil, err
	case w.Auth == nil:
		return nil, fmt.Errorf("%s rejected the token", c.Remote.URL)
	}
	cred.Email, cred.Collection = w.Auth.Email, w.Auth.Collection
	if w.Via == "jwt" {
		cred.Via = "jwt"
	}
	return &LoginResult{Credential: cred, TokenId: w.TokenId}, nil
}

// PasswordLogin signs in with an auth collection's email and password (default collection: _superusers).
type PasswordLogin struct {
	Collection string
	Email      string
	Password   string
	// OTP is asked for when the collection requires a second factor (MFA with OTP). Nil: MFA fails.
	OTP func(email string) (string, error)
	// TokenName names the API token minted on remotes with the tokens plugin.
	TokenName string
}

type authResponse struct {
	Token  string `json:"token"`
	Record struct {
		Id    string `json:"id"`
		Email string `json:"email"`
	} `json:"record"`
}

// LoginWithPassword signs in with email + password, then turns the session into a durable credential: an API token
// minted on the remote when it has the tokens plugin (revocable on its own), else a superuser impersonate token
// (works on any PocketBase; revoking it means rotating the superuser's tokenKey). Regular users without tokens get
// their session JWT, which expires.
func LoginWithPassword(ctx context.Context, c *Client, in PasswordLogin) (*LoginResult, error) {
	if in.Collection == "" {
		in.Collection = "_superusers"
	}
	anon := *c
	anon.Token = ""
	col := url.PathEscape(in.Collection)

	var auth authResponse
	err := anon.JSON(ctx, http.MethodPost, "/api/collections/"+col+"/auth-with-password",
		map[string]string{"identity": in.Email, "password": in.Password}, &auth)
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
		mfaId, _ := ae.Body["mfaId"].(string)
		if mfaId == "" {
			return nil, fmt.Errorf("sign-in failed: %s", ae.Message)
		}
		if in.OTP == nil {
			return nil, errors.New("this account needs a second factor (OTP); run login in a terminal")
		}
		var otp struct {
			OtpId string `json:"otpId"`
		}
		if err := anon.JSON(ctx, http.MethodPost, "/api/collections/"+col+"/request-otp", map[string]string{"email": in.Email}, &otp); err != nil {
			return nil, fmt.Errorf("request a one-time code: %w", err)
		}
		code, err := in.OTP(in.Email)
		if err != nil {
			return nil, err
		}
		if err := anon.JSON(ctx, http.MethodPost, "/api/collections/"+col+"/auth-with-otp",
			map[string]string{"otpId": otp.OtpId, "password": strings.TrimSpace(code), "mfaId": mfaId}, &auth); err != nil {
			return nil, fmt.Errorf("one-time code: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("sign-in failed: %w", err)
	}

	session := *c
	session.Token = auth.Token
	now := time.Now().UTC()
	base := Credential{Email: auth.Record.Email, Collection: in.Collection, Created: now}

	w, werr := session.Whoami(ctx)
	if werr == nil && w.Features["tokens"] != "" {
		name := in.TokenName
		if name == "" {
			host, _ := os.Hostname()
			name = "pocketbase CLI on " + host
		}
		var created struct {
			Token  string `json:"token"`
			Record struct {
				Id string `json:"id"`
			} `json:"record"`
		}
		err := session.JSON(ctx, http.MethodPost, strings.TrimSuffix(w.Features["tokens"], "/"), map[string]any{"name": name}, &created)
		if err == nil {
			cred := base
			cred.Token, cred.Via = created.Token, "token"
			return &LoginResult{Credential: &cred, Minted: true, TokenId: created.Record.Id}, nil
		}
		if !IsStatus(err, http.StatusForbidden) {
			return nil, fmt.Errorf("create an API token: %w", err)
		}
		// this collection may not own tokens: fall through
	}

	if in.Collection == "_superusers" {
		var imp authResponse
		err := session.JSON(ctx, http.MethodPost, "/api/collections/_superusers/impersonate/"+url.PathEscape(auth.Record.Id),
			map[string]any{"duration": int64(ImpersonateTTL / time.Second)}, &imp)
		if err != nil {
			return nil, fmt.Errorf("create a long-lived login: %w", err)
		}
		cred := base
		cred.Token, cred.Via, cred.Expires = imp.Token, "impersonate", now.Add(ImpersonateTTL)
		return &LoginResult{Credential: &cred, Warning: fmt.Sprintf(
			"%s has no API tokens plugin, so this login is a superuser impersonate token valid for %d days. "+
				"It can't be revoked on its own: revoking means changing the superuser's password or tokenKey.",
			c.Remote.URL, int(ImpersonateTTL.Hours()/24))}, nil
	}

	cred := base
	cred.Token, cred.Via = auth.Token, "jwt"
	return &LoginResult{Credential: &cred, Warning: "This login is a session token and will expire; ask for an API token for CI."}, nil
}

// SaveLogin stores a login for the client's remote URL.
func SaveLogin(c *Client, res *LoginResult) error {
	creds, err := LoadCredentials()
	if err != nil {
		return err
	}
	cred := *res.Credential
	if res.Minted {
		cred.MintedTokenId = res.TokenId
	}
	creds.ByURL[c.Remote.URL] = &cred
	return creds.Save()
}
