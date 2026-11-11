// Package remotes gives the pocketbase command named remotes: other PocketBase instances (or PocketHost instances) a
// project deploys to, signed in once with `pocketbase login`.
//
//	pocketbase remote add prod https://myapp.example.com
//	pocketbase login prod
//	pocketbase deploy prod
//
// Remote names and URLs live in the project's .pocketbase/remotes.json (commit it); secrets live in the user's
// ~/.config/pocketbase/credentials.json (0600), keyed by URL. CI sets POCKETBASE_TOKEN or POCKETBASE_TOKEN_<REMOTE>.
//
// The server half is GET /_pocker/whoami: who the request's token is, its scopes, and which features (deploy, OAuth,
// tokens) the instance offers, so clients discover what a remote supports.
package remotes

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// ProjectDir holds the project's remotes file. Not a pb_* name: pb_* folders are deployed.
	ProjectDir  = ".pocketbase"
	ProjectFile = "remotes.json"

	// LegacyPocketHostFile is the npm pockethost CLI's project link ({"instanceName": "..."}). It reads as the
	// remote "pockethost".
	LegacyPocketHostFile = ".pockethostconfig"

	// PocketHostRemote is the name the legacy link reads as.
	PocketHostRemote = "pockethost"

	// DefaultName is the remote used when a command names none and the project sets no default.
	DefaultName = "default"
)

// Remote is one named remote.
type Remote struct {
	Name string `json:"-"`
	URL  string `json:"url"`
}

// Project is a project's remotes file.
type Project struct {
	Default string             `json:"default,omitempty"`
	Remotes map[string]*Remote `json:"remotes"`

	// Root is the project folder (where .pocketbase/ is, or will be created).
	Root string `json:"-"`
	// legacy: remotes read from .pockethostconfig, not written back
	legacy map[string]bool
}

var nameRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

// ValidName reports whether s can name a remote.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// FindProjectRoot walks up from dir to the nearest folder with .pocketbase/remotes.json or .pockethostconfig. None:
// dir itself.
func FindProjectRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for d := abs; ; {
		if exists(filepath.Join(d, ProjectDir, ProjectFile)) || exists(filepath.Join(d, LegacyPocketHostFile)) {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return abs
		}
		d = parent
	}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// LoadProject reads the remotes file of the project containing dir (see [FindProjectRoot]). A project with no file
// has no remotes.
func LoadProject(dir string) (*Project, error) {
	root := FindProjectRoot(dir)
	p := &Project{Root: root, Remotes: map[string]*Remote{}, legacy: map[string]bool{}}
	b, err := os.ReadFile(filepath.Join(root, ProjectDir, ProjectFile))
	switch {
	case err == nil:
		if err := json.Unmarshal(b, p); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(ProjectDir, ProjectFile), err)
		}
		if p.Remotes == nil {
			p.Remotes = map[string]*Remote{}
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	for name, r := range p.Remotes {
		r.Name = name
	}
	if _, ok := p.Remotes[PocketHostRemote]; !ok {
		if u := readPocketHostLink(root); u != "" {
			p.Remotes[PocketHostRemote] = &Remote{Name: PocketHostRemote, URL: u}
			p.legacy[PocketHostRemote] = true
		}
	}
	return p, nil
}

// readPocketHostLink turns .pockethostconfig's instance name into its URL (https://<name>.pockethost.io; a name
// with a dot is a custom domain).
func readPocketHostLink(root string) string {
	b, err := os.ReadFile(filepath.Join(root, LegacyPocketHostFile))
	if err != nil {
		return ""
	}
	var c struct {
		InstanceName string `json:"instanceName"`
	}
	if json.Unmarshal(b, &c) != nil || c.InstanceName == "" {
		return ""
	}
	if strings.Contains(c.InstanceName, ".") {
		return "https://" + c.InstanceName
	}
	return "https://" + c.InstanceName + ".pockethost.io"
}

// Save writes the remotes file (legacy remotes stay in .pockethostconfig).
func (p *Project) Save() error {
	out := Project{Default: p.Default, Remotes: map[string]*Remote{}}
	for name, r := range p.Remotes {
		if !p.legacy[name] {
			out.Remotes[name] = r
		}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(p.Root, ProjectDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ProjectFile), append(b, '\n'), 0o644)
}

// Names returns the remote names, sorted.
func (p *Project) Names() []string {
	out := make([]string, 0, len(p.Remotes))
	for n := range p.Remotes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Add adds or replaces a remote. The first remote becomes the default.
func (p *Project) Add(name, rawURL string) (*Remote, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid remote name %q (letters, digits, _ . -)", name)
	}
	u, err := NormalizeURL(rawURL)
	if err != nil {
		return nil, err
	}
	r := &Remote{Name: name, URL: u}
	p.Remotes[name] = r
	delete(p.legacy, name)
	if p.Default == "" {
		p.Default = name
	}
	return r, nil
}

// Remove deletes a remote.
func (p *Project) Remove(name string) error {
	if _, ok := p.Remotes[name]; !ok {
		return fmt.Errorf("no remote %q", name)
	}
	if p.legacy[name] {
		return fmt.Errorf("remote %q comes from %s; delete that file instead", name, LegacyPocketHostFile)
	}
	delete(p.Remotes, name)
	if p.Default == name {
		p.Default = ""
	}
	return nil
}

// Resolve picks a remote: by name, else the project default, else the only remote, else "default". A URL works as a
// name too (an unnamed remote).
func (p *Project) Resolve(name string) (*Remote, error) {
	if name == "" {
		name = p.Default
	}
	if name == "" {
		if len(p.Remotes) == 1 {
			for _, r := range p.Remotes {
				return r, nil
			}
		}
		name = DefaultName
	}
	if r, ok := p.Remotes[name]; ok {
		return r, nil
	}
	if strings.Contains(name, "://") {
		u, err := NormalizeURL(name)
		if err != nil {
			return nil, err
		}
		return &Remote{Name: u, URL: u}, nil
	}
	if len(p.Remotes) == 0 {
		return nil, fmt.Errorf("no remotes yet: run 'pocketbase remote add %s <url>' or 'pocketbase link <url>'", name)
	}
	return nil, fmt.Errorf("no remote %q (have: %s)", name, strings.Join(p.Names(), ", "))
}

// NormalizeURL checks an instance URL and trims it to scheme://host[:port][/path] without a trailing slash. A bare
// host gets https://.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty URL")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid URL %q", raw)
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return strings.TrimRight(u.String(), "/"), nil
}

// ---------------------------------------------------------------------------------------------------------------
// credentials

// Credential is a saved sign-in for one URL.
type Credential struct {
	Token string `json:"token"`
	// Via: "token" (pasted or minted pbt_/PocketHost token), "impersonate" (superuser JWT), "oauth".
	Via          string    `json:"via"`
	Email        string    `json:"email,omitempty"`
	Collection   string    `json:"collection,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	Expires      time.Time `json:"expires,omitzero"`
	// MintedTokenId is the API token the CLI created on the remote at login; logout revokes it.
	MintedTokenId string    `json:"mintedTokenId,omitempty"`
	Created       time.Time `json:"created"`
}

// Credentials is the user's credential store.
type Credentials struct {
	path  string
	ByURL map[string]*Credential `json:"credentials"`
}

// ConfigDir is the user's pocketbase config folder: $POCKETBASE_CONFIG_DIR, else $XDG_CONFIG_HOME/pocketbase, else
// ~/.config/pocketbase.
func ConfigDir() (string, error) {
	if d := os.Getenv("POCKETBASE_CONFIG_DIR"); d != "" {
		return d, nil
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "pocketbase"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "pocketbase"), nil
}

// LoadCredentials reads the credential store (empty if there's none).
func LoadCredentials() (*Credentials, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	c := &Credentials{path: filepath.Join(dir, "credentials.json"), ByURL: map[string]*Credential{}}
	b, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s: %w", c.path, err)
	}
	if c.ByURL == nil {
		c.ByURL = map[string]*Credential{}
	}
	return c, nil
}

// Save writes the store with 0600 permissions.
func (c *Credentials) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Path is the store's file.
func (c *Credentials) Path() string { return c.path }

// EnvToken returns the CI token for a remote: POCKETBASE_TOKEN_<NAME> (upper case, other characters as _), then
// POCKETBASE_TOKEN; for the legacy pockethost remote, POCKETHOST_TOKEN too.
func EnvToken(remoteName string) (token, source string) {
	if remoteName != "" && !strings.Contains(remoteName, "://") {
		key := "POCKETBASE_TOKEN_" + envSuffix(remoteName)
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v, key
		}
	}
	if v := strings.TrimSpace(os.Getenv("POCKETBASE_TOKEN")); v != "" {
		return v, "POCKETBASE_TOKEN"
	}
	if remoteName == PocketHostRemote {
		if v := strings.TrimSpace(os.Getenv("POCKETHOST_TOKEN")); v != "" {
			return v, "POCKETHOST_TOKEN"
		}
	}
	return "", ""
}

func envSuffix(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
