package pages

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/tests"
)

// TestAuthKitEmailFlows runs the auth starter kit's password reset, email change and resend-verification pages end
// to end: form posts, the email PocketBase sends (captured by the test mailer), and the confirm pages.
func TestAuthKitEmailFlows(t *testing.T) {
	h, p := newServer(t, "starters/auth/pb_pages")
	mailer := p.app.(*tests.TestApp).TestMailer

	type resp struct {
		code     int
		body     string
		location string
		cookies  []*http.Cookie
	}
	jar := map[string]string{}
	do := func(method, path string, form url.Values) resp {
		t.Helper()
		var req *http.Request
		if form != nil {
			req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		var parts []string
		for k, v := range jar {
			parts = append(parts, k+"="+v)
		}
		if len(parts) > 0 {
			req.Header.Set("Cookie", strings.Join(parts, "; "))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		for _, c := range rec.Result().Cookies() {
			if c.MaxAge < 0 || c.Value == "" {
				delete(jar, c.Name)
			} else {
				jar[c.Name] = c.Value
			}
		}
		return resp{rec.Code, rec.Body.String(), rec.Header().Get("Location"), rec.Result().Cookies()}
	}
	// waitMail waits for the next message (mail is sent in the background) and returns the token in its link.
	waitMail := func(n int, linkRe *regexp.Regexp) string {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for mailer.TotalSend() < n {
			if time.Now().After(deadline) {
				t.Fatalf("no email (sent %d, want %d)", mailer.TotalSend(), n)
			}
			time.Sleep(10 * time.Millisecond)
		}
		m := mailer.LastMessage()
		match := linkRe.FindStringSubmatch(m.HTML)
		if match == nil {
			t.Fatalf("no link matching %s in %q", linkRe, m.HTML)
		}
		return match[1]
	}
	sent := 0

	// ---- forgot password → reset link → new password ----
	if r := do("POST", "/auth/forgot", url.Values{"identity": {"nope"}}); r.code != 200 || !strings.Contains(r.body, "Enter the email address") {
		t.Fatalf("forgot with no email: %d %s", r.code, r.body)
	}
	if r := do("POST", "/auth/forgot", url.Values{"identity": {"nobody@example.com"}}); r.code != 302 || r.location != "/auth/login" {
		t.Fatalf("forgot for an unknown address must look the same: %d %s", r.code, r.location)
	}
	if r := do("POST", "/auth/forgot", url.Values{"identity": {"test@example.com"}}); r.code != 302 || r.location != "/auth/login" {
		t.Fatalf("forgot: %d %s", r.code, r.location)
	}
	sent++
	tok := waitMail(sent, regexp.MustCompile(`confirm-password-reset/([^"'<\s]+)`))
	if mailer.TotalSend() != sent {
		t.Fatalf("an unknown address got mail: %d sent", mailer.TotalSend())
	}

	reset := "/auth/confirm/" + tok + "/password-reset"
	if r := do("GET", reset, nil); r.code != 200 || !strings.Contains(r.body, "Choose a New Password") {
		t.Fatalf("reset page: %d %s", r.code, r.body)
	}
	if r := do("POST", reset, url.Values{"password": {"newpassword123"}, "passwordConfirm": {"different123"}}); r.code != 200 || !strings.Contains(r.body, "match") {
		t.Fatalf("mismatched passwords: %d %s", r.code, r.body)
	}
	if r := do("POST", "/auth/confirm/bogus/password-reset", url.Values{"password": {"newpassword123"}, "passwordConfirm": {"newpassword123"}}); r.code != 200 || !strings.Contains(r.body, "Invalid or expired") {
		t.Fatalf("bad token: %d %s", r.code, r.body)
	}
	if r := do("POST", reset, url.Values{"password": {"newpassword123"}, "passwordConfirm": {"newpassword123"}}); r.code != 302 || r.location != "/auth/login" {
		t.Fatalf("reset: %d %s %s", r.code, r.location, r.body)
	}
	user, err := p.app.FindAuthRecordByEmail("users", "test@example.com")
	if err != nil || !user.ValidatePassword("newpassword123") {
		t.Fatalf("password not changed: %v", err)
	}

	// ---- sign in, change email ----
	if r := do("POST", "/auth/login", url.Values{"mode": {"password"}, "identity": {"test@example.com"}, "password": {"newpassword123"}}); r.code != 302 || jar["pb_auth"] == "" {
		t.Fatalf("login: %d %s %s", r.code, r.location, r.body)
	}
	for bad, want := range map[string]string{"not-an-email": "valid email", "test@example.com": "already your", "test2@example.com": "already in use"} {
		if r := do("POST", "/account/change-email", url.Values{"email": {bad}}); r.code != 200 || !strings.Contains(r.body, want) {
			t.Fatalf("change to %q: %d %s", bad, r.code, r.body)
		}
	}
	if mailer.TotalSend() != sent {
		t.Fatal("a rejected email change sent mail")
	}
	if r := do("POST", "/account/change-email", url.Values{"email": {"fresh@example.com"}}); r.code != 302 || r.location != "/account" {
		t.Fatalf("change email: %d %s %s", r.code, r.location, r.body)
	}
	sent++
	tok = waitMail(sent, regexp.MustCompile(`confirm-email-change/([^"'<\s]+)`))
	if to := mailer.LastMessage().To; len(to) != 1 || to[0].Address != "fresh@example.com" {
		t.Fatalf("confirmation went to %v, want the new address", to)
	}
	change := "/auth/confirm/" + tok + "/email-change"
	if r := do("POST", change, url.Values{"password": {"wrong"}}); r.code != 200 || !strings.Contains(r.body, "wrong password") {
		t.Fatalf("wrong password: %d %s", r.code, r.body)
	}
	if r := do("POST", change, url.Values{"password": {"newpassword123"}}); r.code != 302 || r.location != "/account" {
		t.Fatalf("confirm change: %d %s %s", r.code, r.location, r.body)
	}
	if _, err := p.app.FindAuthRecordByEmail("users", "fresh@example.com"); err != nil {
		t.Fatalf("email not changed: %v", err)
	}
	if r := do("GET", "/account", nil); r.code != 200 || !strings.Contains(r.body, "fresh@example.com") {
		t.Fatalf("still signed in with the new address: %d %s", r.code, r.body)
	}

	// ---- register (unverified), resend verification, follow the link ----
	do("GET", "/auth/logout", nil)
	if r := do("POST", "/auth/register", url.Values{"identity": {"newbie@example.com"}, "password": {"password123"}}); r.code != 302 {
		t.Fatalf("register: %d %s", r.code, r.body)
	}
	sent++ // register sends the first verification email
	waitMail(sent, regexp.MustCompile(`confirm-verification/([^"'<\s]+)`))
	if r := do("GET", "/auth/verify", nil); r.code != 200 || !strings.Contains(r.body, "newbie@example.com") {
		t.Fatalf("verify page: %d %s", r.code, r.body)
	}
	if r := do("POST", "/auth/verify", nil); r.code != 302 || r.location != "/account" {
		t.Fatalf("resend: %d %s %s", r.code, r.location, r.body)
	}
	sent++
	tok = waitMail(sent, regexp.MustCompile(`confirm-verification/([^"'<\s]+)`))
	if r := do("GET", "/auth/confirm/"+tok+"/verification", nil); r.code != 302 || r.location != "/" {
		t.Fatalf("verification link: %d %s %s", r.code, r.location, r.body)
	}
	if u, _ := p.app.FindAuthRecordByEmail("users", "newbie@example.com"); u == nil || !u.Verified() {
		t.Fatal("not verified")
	}
	if r := do("GET", "/auth/verify", nil); r.code != 302 || r.location != "/account" {
		t.Fatalf("verify page once verified should redirect: %d %s", r.code, r.location)
	}
}
