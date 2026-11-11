package deploy

import (
	"errors"
	"slices"
)

// Auth is who a deploy request acts as.
type Auth struct {
	UserID string
	// Scopes: "read" (whoami, files, logs) and "write" (deploy, upload, delete).
	Scopes     []string
	Via        string // how it authenticated, e.g. "token", "jwt", "pat"
	TokenLabel string // the token's name, recorded with each deploy
}

func (a Auth) can(scope string) bool { return slices.Contains(a.Scopes, scope) }

// ReadWrite are the scopes of full deploy access.
var ReadWrite = []string{"read", "write"}

// AuthError is a refused credential, answered with Status (401 or 403) and Message.
type AuthError struct {
	Status  int
	Message string
}

func (e *AuthError) Error() string { return e.Message }

// ErrNoAccess from [Backend.Authenticate] answers 404, as if the instance didn't exist.
var ErrNoAccess = errors.New("no access to this instance")
