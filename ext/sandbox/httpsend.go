package sandbox

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/dop251/goja"
)

// errBodyTooLarge is what $http.send reports when a response is over HTTPSendMaxBody.
var errBodyTooLarge = errors.New("$http.send: response body larger than the allowed size")

// HTTPClient is the app's client for $http.send (a jsvm.Extension method).
func (s *Sandbox) HTTPClient(*goja.Runtime) *http.Client { return s.httpClient() }

// httpClient is the app's client for $http.send: it dials through Go's default transport (so
// the egress policy applies when installed) with this app's timeout and response size caps.
func (s *Sandbox) httpClient() *http.Client {
	s.clientOnce.Do(func() {
		c := &http.Client{Transport: &bodyCapTransport{max: s.HTTPSendMaxBody}}
		if s.HTTPSendMaxTimeout > 0 {
			c.Timeout = time.Duration(s.HTTPSendMaxTimeout) * time.Second // the caller's own timeout still applies when shorter
		}
		s.client = c
	})
	return s.client
}

// bodyCapTransport reads each response body up front (at most max+1 bytes) and fails the request when it's larger,
// so the error reaches the JS caller instead of a silently truncated body.
type bodyCapTransport struct {
	max  int64
	base http.RoundTripper // nil = http.DefaultTransport, looked up per request so a later InstallEgressPolicy applies
}

func (t *bodyCapTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	res, err := base.RoundTrip(req)
	if err != nil || t.max <= 0 {
		return res, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, t.max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > t.max {
		return nil, errBodyTooLarge
	}
	res.Body = io.NopCloser(bytes.NewReader(body))
	res.ContentLength = int64(len(body))
	return res, nil
}
