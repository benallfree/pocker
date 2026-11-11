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

// LoginResult is a finished sign-in, ready to save.
type LoginResult struct {
	Credential *Credential
	// Warning, if set, is shown to the user .
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

// ErrNoTokens: the remote has no tokens plugin, so a password sign-in can't become a revocable API token.
var ErrNoTokens = errors.New("the remote has no API tokens plugin")

// LoginWithPassword signs in with email + password, then mints an API token on the remote (named after this
// machine; logout revokes it). The remote must have the tokens plugin: the session itself is never saved, since
// it expires and can't be revoked on its own.
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
	w, err := session.Whoami(ctx)
	if errors.Is(err, ErrNoWhoami) || (err == nil && w.Features["tokens"] == "") {
		return nil, fmt.Errorf("%s: %w; paste an API token instead (login --token)", c.Remote.URL, ErrNoTokens)
	}
	if err != nil {
		return nil, err
	}
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
	if err := session.JSON(ctx, http.MethodPost, strings.TrimSuffix(w.Features["tokens"], "/"), map[string]any{"name": name}, &created); err != nil {
		return nil, fmt.Errorf("create an API token: %w", err)
	}
	cred := &Credential{Token: created.Token, Via: "token", Email: auth.Record.Email, Collection: in.Collection, Created: time.Now().UTC()}
	return &LoginResult{Credential: cred, Minted: true, TokenId: created.Record.Id}, nil
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
