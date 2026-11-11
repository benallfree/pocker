package pages

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/mails"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/routine"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/subscriptions"
	"github.com/dop251/goja"
)

// ---- ctx.res.sse() ----

// sse starts a text/event-stream response and returns the stream object for JS.
func (r *request) sse() *goja.Object {
	if r.sent {
		panic(r.vm.NewGoError(errors.New("ctx.res.sse(): a response was already sent")))
	}
	r.sent = true
	h := r.e.Response.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	r.e.Response.WriteHeader(http.StatusOK)
	_ = r.e.Flush()

	closed := false
	write := func(s string) {
		if closed || r.isHead() {
			return
		}
		_, _ = r.e.Response.Write([]byte(s))
		_ = r.e.Flush()
	}
	o := r.vm.NewObject()
	_ = o.Set("send", func(event string, data goja.Value, opts map[string]any) {
		var b strings.Builder
		if id, ok := opts["id"]; ok && id != nil && fmt.Sprint(id) != "" {
			fmt.Fprintf(&b, "id: %v\n", id)
		}
		if retry, ok := toInt(opts["retry"]); ok && retry > 0 {
			fmt.Fprintf(&b, "retry: %d\n", retry)
		}
		if event != "" {
			fmt.Fprintf(&b, "event: %s\n", event)
		}
		text := ""
		if data != nil && !goja.IsUndefined(data) && !goja.IsNull(data) {
			if s, ok := data.Export().(string); ok {
				text = s
			} else {
				j, err := marshalJSON(data.Export())
				if err != nil {
					panic(r.vm.NewGoError(err))
				}
				text = string(j)
			}
		}
		for _, line := range strings.Split(text, "\n") {
			fmt.Fprintf(&b, "data: %s\n", line)
		}
		b.WriteString("\n")
		write(b.String())
	})
	_ = o.Set("comment", func(text string) {
		var b strings.Builder
		for _, line := range strings.Split(text, "\n") {
			fmt.Fprintf(&b, ": %s\n", line)
		}
		b.WriteString("\n")
		write(b.String())
	})
	_ = o.Set("close", func() { closed = true })
	return o
}

// ---- realtime ----

func (r *request) realtimeObject() *goja.Object {
	vm := r.vm
	broker := r.p.app.SubscriptionsBroker()
	o := vm.NewObject()
	_ = o.Set("send", func(topic string, data goja.Value, opts map[string]any) {
		var v any
		if data != nil {
			v = data.Export()
		}
		payload, err := marshalJSON(v)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		var filter goja.Callable
		if f, ok := opts["filter"]; ok && f != nil {
			fv := vm.ToValue(f)
			filter, _ = goja.AssertFunction(fv)
		}
		msg := subscriptions.Message{Name: topic, Data: payload}
		for _, client := range broker.Clients() {
			if !client.HasSubscription(topic) {
				continue
			}
			if filter != nil {
				ok, err := filter(goja.Undefined(), vm.ToValue(client))
				if err != nil {
					panic(err)
				}
				if !ok.ToBoolean() {
					continue
				}
			}
			c := client
			routine.FireAndForget(func() { c.Send(msg) }) // Send blocks until the client reads; never stall the page
		}
	})
	_ = o.Set("client", func(id string) goja.Value {
		c, err := broker.ClientById(id)
		if err != nil || c == nil {
			return goja.Null()
		}
		return vm.ToValue(c)
	})
	return o
}

// ---- auth (ctx.session) ----

const authCookie = "pb_auth"

type authCookieValue struct {
	Token  string       `json:"token"`
	Record *core.Record `json:"record"`
}

// readAuthCookie sets r.auth from a valid pb_auth cookie, or clears a bad one.
func (r *request) readAuthCookie() {
	raw, ok := r.cookies()[authCookie]
	if !ok || raw == "" {
		return
	}
	var v struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil || v.Token == "" {
		r.setCookie(authCookie, "", map[string]any{"maxAge": 0})
		return
	}
	rec, err := r.p.app.FindAuthRecordByToken(v.Token, core.TokenTypeAuth)
	if err != nil || rec == nil {
		r.setCookie(authCookie, "", map[string]any{"maxAge": 0})
		return
	}
	r.auth = rec
	r.e.Auth = rec
}

func (r *request) authCollection(opts map[string]any) *core.Collection {
	name := "users"
	if c, ok := opts["collection"].(string); ok && c != "" {
		name = c
	}
	col, err := r.p.app.FindCollectionByNameOrId(name)
	if err != nil || !col.IsAuth() {
		panic(r.vm.NewGoError(fmt.Errorf("ctx.session: %q is not an auth collection", name)))
	}
	return col
}

func (r *request) fail400(msg string) {
	panic(r.vm.NewGoError(errors.New(msg)))
}

// signIn sets the session cookie for record and returns { token, record }.
func (r *request) signIn(rec *core.Record) map[string]any {
	token, err := rec.NewAuthToken()
	if err != nil {
		panic(r.vm.NewGoError(err))
	}
	val, err := json.Marshal(authCookieValue{Token: token, Record: rec})
	if err != nil {
		panic(r.vm.NewGoError(err))
	}
	maxAge := int(rec.Collection().AuthToken.DurationTime() / time.Second)
	r.setCookie(authCookie, string(val), map[string]any{"maxAge": maxAge})
	r.auth = rec
	r.e.Auth = rec
	return map[string]any{"token": token, "record": rec}
}

func (r *request) findByIdentity(col *core.Collection, identity string) *core.Record {
	fields := col.PasswordAuth.IdentityFields
	if len(fields) == 0 {
		fields = []string{core.FieldNameEmail}
	}
	for _, f := range fields {
		rec, err := r.p.app.FindFirstRecordByData(col, f, identity)
		if err == nil && rec != nil {
			return rec
		}
	}
	return nil
}

func (r *request) sessionObject() *goja.Object {
	vm := r.vm
	app := r.p.app
	o := vm.NewObject()
	set := func(name string, v any) { _ = o.Set(name, v) }

	set("current", func() goja.Value {
		if r.auth == nil {
			return goja.Null()
		}
		return vm.ToValue(r.auth)
	})

	set("authMethods", func(opts map[string]any) map[string]any {
		col := r.authCollection(opts)
		providers := []any{}
		for _, p := range col.OAuth2.Providers {
			providers = append(providers, map[string]any{"name": p.Name, "displayName": p.DisplayName})
		}
		return map[string]any{
			"password": map[string]any{"enabled": col.PasswordAuth.Enabled, "identityFields": col.PasswordAuth.IdentityFields},
			"otp":      map[string]any{"enabled": col.OTP.Enabled},
			"mfa":      map[string]any{"enabled": col.MFA.Enabled},
			"oauth2":   map[string]any{"enabled": col.OAuth2.Enabled, "providers": providers},
		}
	})

	set("signIn", func(rec *core.Record) map[string]any {
		if rec == nil || !rec.Collection().IsAuth() {
			r.fail400("ctx.session.signIn: needs an auth record")
		}
		return r.signIn(rec)
	})

	set("signInWithPassword", func(identity, password string, opts map[string]any) map[string]any {
		col := r.authCollection(opts)
		if !col.PasswordAuth.Enabled {
			r.fail400("password sign-in is turned off for " + col.Name)
		}
		rec := r.findByIdentity(col, identity)
		if rec == nil || !rec.ValidatePassword(password) {
			r.fail400("Invalid login credentials.")
		}
		return r.signIn(rec)
	})

	set("register", func(email, password string, opts map[string]any) map[string]any {
		col := r.authCollection(opts)
		rec := core.NewRecord(col)
		if data, ok := opts["data"].(map[string]any); ok {
			for k, v := range data {
				rec.Set(k, v)
			}
		}
		rec.SetEmail(email)
		rec.SetPassword(password)
		if err := app.Save(rec); err != nil {
			panic(vm.NewGoError(err))
		}
		if v, ok := opts["verify"].(bool); !ok || v {
			if err := mails.SendRecordVerification(app, rec); err != nil {
				app.Logger().Error("pages: verification email failed", "error", err)
			}
		}
		if v, ok := opts["signIn"].(bool); !ok || v {
			return r.signIn(rec)
		}
		return map[string]any{"token": "", "record": rec}
	})

	set("signInAnonymously", func(opts map[string]any) map[string]any {
		col := r.authCollection(opts)
		rec := core.NewRecord(col)
		rec.SetEmail("anon-" + strings.ToLower(security.RandomString(16)) + "@anonymous.invalid")
		rec.SetPassword(security.RandomString(32))
		if err := app.Save(rec); err != nil {
			panic(vm.NewGoError(err))
		}
		return r.signIn(rec)
	})

	set("requestOTP", func(email string, opts map[string]any) string {
		col := r.authCollection(opts)
		if !col.OTP.Enabled {
			r.fail400("OTP sign-in is turned off for " + col.Name)
		}
		rec, err := app.FindAuthRecordByEmail(col, email)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			panic(vm.NewGoError(err))
		}
		if rec == nil {
			if c, _ := opts["create"].(bool); !c {
				return core.GenerateDefaultRandomId()
			}
			rec = core.NewRecord(col)
			rec.SetEmail(email)
			rec.SetPassword(security.RandomString(32))
			if err := app.Save(rec); err != nil {
				panic(vm.NewGoError(err))
			}
		}
		pass := security.RandomStringWithAlphabet(col.OTP.Length, "1234567890")
		otp := core.NewOTP(app)
		otp.SetCollectionRef(col.Id)
		otp.SetRecordRef(rec.Id)
		otp.SetPassword(pass)
		otp.SetSentTo(email)
		if err := app.Save(otp); err != nil {
			panic(vm.NewGoError(err))
		}
		routine.FireAndForget(func() {
			if err := mails.SendRecordOTP(app, rec, otp.Id, pass); err != nil {
				app.Logger().Error("pages: OTP email failed", "error", errors.Join(err, app.Delete(otp)))
			}
		})
		return otp.Id
	})

	set("signInWithOTP", func(otpId, code string, opts map[string]any) map[string]any {
		col := r.authCollection(opts)
		otp, err := app.FindOTPById(otpId)
		if err != nil || otp.CollectionRef() != col.Id || otp.HasExpired(col.OTP.DurationTime()) || !otp.ValidatePassword(code) {
			r.fail400("Invalid or expired one-time password.")
		}
		rec, err := app.FindRecordById(col, otp.RecordRef())
		if err != nil {
			r.fail400("Invalid or expired one-time password.")
		}
		_ = app.Delete(otp)
		if !rec.Verified() && otp.SentTo() != "" && strings.EqualFold(otp.SentTo(), rec.Email()) {
			rec.SetVerified(true)
			_ = app.Save(rec)
		}
		return r.signIn(rec)
	})

	set("signOut", func() {
		r.setCookie(authCookie, "", map[string]any{"maxAge": 0})
		r.auth = nil
		r.e.Auth = nil
	})

	set("requestVerification", func(email string, opts map[string]any) {
		col := r.authCollection(opts)
		rec, err := app.FindAuthRecordByEmail(col, email)
		if err != nil || rec.Verified() {
			return // never reveal whether the email exists
		}
		routine.FireAndForget(func() {
			if err := mails.SendRecordVerification(app, rec); err != nil {
				app.Logger().Error("pages: verification email failed", "error", err)
			}
		})
	})

	set("confirmVerification", func(token string) goja.Value {
		rec, err := app.FindAuthRecordByToken(token, core.TokenTypeVerification)
		if err != nil || !tokenEmailMatches(token, rec.Email()) {
			r.fail400("Invalid or expired verification token.")
		}
		if !rec.Verified() {
			rec.SetVerified(true)
			if err := app.Save(rec); err != nil {
				panic(vm.NewGoError(err))
			}
		}
		return vm.ToValue(rec)
	})

	set("requestPasswordReset", func(email string, opts map[string]any) {
		col := r.authCollection(opts)
		rec, err := app.FindAuthRecordByEmail(col, email)
		if err != nil {
			return
		}
		routine.FireAndForget(func() {
			if err := mails.SendRecordPasswordReset(app, rec); err != nil {
				app.Logger().Error("pages: password reset email failed", "error", err)
			}
		})
	})

	set("confirmPasswordReset", func(token, password, confirm string) goja.Value {
		if password != confirm {
			r.fail400("The passwords don't match.")
		}
		rec, err := app.FindAuthRecordByToken(token, core.TokenTypePasswordReset)
		if err != nil || !tokenEmailMatches(token, rec.Email()) {
			r.fail400("Invalid or expired password reset token.")
		}
		rec.SetPassword(password)
		if !rec.Verified() {
			rec.SetVerified(true)
		}
		if err := app.Save(rec); err != nil {
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(rec)
	})

	set("requestEmailChange", func(newEmail string) {
		if r.auth == nil {
			r.fail400("ctx.session.requestEmailChange: not signed in")
		}
		rec := r.auth
		routine.FireAndForget(func() {
			if err := mails.SendRecordChangeEmail(app, rec, newEmail); err != nil {
				app.Logger().Error("pages: email change email failed", "error", err)
			}
		})
	})

	set("confirmEmailChange", func(token, password string) goja.Value {
		rec, err := app.FindAuthRecordByToken(token, core.TokenTypeEmailChange)
		if err != nil || !tokenEmailMatches(token, rec.Email()) || !rec.ValidatePassword(password) {
			r.fail400("Invalid or expired email change token, or wrong password.")
		}
		newEmail := tokenClaim(token, core.TokenClaimNewEmail)
		if newEmail == "" {
			r.fail400("Invalid email change token.")
		}
		rec.SetEmail(newEmail)
		rec.SetVerified(true)
		if err := app.Save(rec); err != nil {
			panic(vm.NewGoError(err))
		}
		if r.auth != nil && r.auth.Id == rec.Id {
			r.signIn(rec)
		}
		return vm.ToValue(rec)
	})

	set("requestOAuth2", func(provider string, opts map[string]any) string {
		return r.requestOAuth2(provider, opts)
	})
	set("signInWithOAuth2", func(state, code string, opts map[string]any) map[string]any {
		return r.signInWithOAuth2(state, code, opts)
	})
	return o
}

// tokenClaim reads a claim from a token that was already verified.
func tokenClaim(token, claim string) string {
	claims, err := security.ParseUnverifiedJWT(token)
	if err != nil {
		return ""
	}
	s, _ := claims[claim].(string)
	return s
}

// tokenEmailMatches checks a token's email claim against the record's current email (PocketBase's own check for
// verification, password reset and email change tokens).
func tokenEmailMatches(token, email string) bool {
	return strings.EqualFold(tokenClaim(token, core.TokenClaimEmail), email)
}

// marshalJSON is json.Marshal without HTML escaping (payloads often carry HTML).
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
