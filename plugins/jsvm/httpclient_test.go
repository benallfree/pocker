package jsvm

// CE: $http.send uses the runtime's own *http.Client when one is set under HTTPClientKey.

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

type cannedTransport struct{ calls int }

func (c *cannedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.calls++
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": {"text/plain"}},
		Body:       io.NopCloser(strings.NewReader("from the app's client")),
		Request:    req,
	}, nil
}

func TestBindHTTPUsesRuntimeClient(t *testing.T) {
	vm := goja.New()
	BindHTTP(vm)
	tr := &cannedTransport{}
	vm.Set(HTTPClientKey, &http.Client{Transport: tr})

	// no field mapper on this bare runtime, so the Go field name
	v, err := vm.RunString(`$http.send({ url: "http://example.invalid/x", timeout: 3 }).Raw`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "from the app's client" || tr.calls != 1 {
		t.Fatalf("raw = %q, calls = %d", got, tr.calls)
	}
}
