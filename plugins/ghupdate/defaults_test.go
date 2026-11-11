package ghupdate

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"ganigeorgiev.com/pocketbase/pocketbase/tests"
	"github.com/spf13/cobra"
)

type recordingClient struct{ urls []string }

func (c *recordingClient) Do(req *http.Request) (*http.Response, error) {
	c.urls = append(c.urls, req.URL.String())
	return nil, errors.New("stop after the first request")
}

func latestURL(t *testing.T, config Config) string {
	t.Helper()

	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()

	client := &recordingClient{}
	config.HttpClient = client
	config.Context = context.Background()

	root := &cobra.Command{Use: "pocketbase", Version: "0.0.1"}
	if err := Register(app, root, config); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"update"})
	_ = root.Execute()

	if len(client.urls) != 1 {
		t.Fatalf("expected 1 request, got %v", client.urls)
	}
	return client.urls[0]
}

func TestDefaultsPointAtPocketBaseCE(t *testing.T) {
	got := latestURL(t, Config{})
	want := "https://api.github.com/repos/pockethost/pocketbase/releases/latest"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestLinkTimeDefaultsOverride(t *testing.T) {
	owner, repo, base := DefaultOwner, DefaultRepo, DefaultBaseURL
	t.Cleanup(func() { DefaultOwner, DefaultRepo, DefaultBaseURL = owner, repo, base })

	// what -ldflags "-X .../plugins/ghupdate.DefaultOwner=acme ..." does
	DefaultOwner, DefaultRepo, DefaultBaseURL = "acme", "acme-pb", "https://git.example.com/api/v3/"

	got := latestURL(t, Config{})
	want := "https://git.example.com/api/v3/repos/acme/acme-pb/releases/latest"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestConfigBeatsDefaults(t *testing.T) {
	got := latestURL(t, Config{Owner: "me", Repo: "mine", BaseURL: "https://gh.example.com"})
	want := "https://gh.example.com/repos/me/mine/releases/latest"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
