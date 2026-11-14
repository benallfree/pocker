package jsvm

// PocketOne: $http.send uses the runtime's own *http.Client when one is set under httpClientKey.

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

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

type clientExtension struct{ c *http.Client }

func (e clientExtension) HTTPClient(*goja.Runtime) *http.Client { return e.c }

// $http.send uses the client an extension supplies, and JS can't reach that client's fields to lift its limits.
func TestBindHTTPUsesExtensionClient(t *testing.T) {
	vm := goja.New()
	vm.SetFieldNameMapper(FieldMapper{})
	BindHTTP(vm)
	tr := &cannedTransport{}
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	extensions{clientExtension{c}}.onInit(vm)

	// the attacks: replace the property, or reach into the client to zero the timeout or drop the transport
	_, _ = vm.RunString(`try { __jsvm_http_client = null } catch (e) {}`)
	_, _ = vm.RunString(`try { __jsvm_http_client.timeout = 0 } catch (e) {}`)
	_, _ = vm.RunString(`try { __jsvm_http_client.transport = null } catch (e) {}`)
	_, _ = vm.RunString(`try { __jsvm_http_client.c = null } catch (e) {}`)
	if c.Timeout != 5*time.Second || c.Transport != tr {
		t.Fatalf("JS changed the client: timeout %v, transport replaced %v", c.Timeout, c.Transport != tr)
	}

	v, err := vm.RunString(`$http.send({ url: "http://example.invalid/x", timeout: 3 }).raw`)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.String(); got != "from the app's client" || tr.calls != 1 {
		t.Fatalf("raw = %q, calls = %d", got, tr.calls)
	}
}
