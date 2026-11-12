package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// MothershipProvider reads the edge list from a mothership: a PocketBase app with an `edges` collection whose
// records have edge_id, internal_url, region, status and last_seen fields. It logs in as a superuser (the edge's
// local config carries the login) and re-reads the collection every Refresh. Until the first read succeeds it knows
// no edges.
//
// An instance's Remote names its owner as "edge:<record id>", "edge:@<edge_id>" or just "<edge_id>".
type MothershipProvider struct {
	URL        string        // e.g. http://10.88.0.10:8090
	Email      string        // superuser login
	Password   string
	Collection string        // default "edges"
	SelfID     string        // this edge's edge_id
	Refresh    time.Duration // default 30s
	Client     *http.Client  // default: its own transport (not http.DefaultTransport, which may carry an egress policy)
	Logger     *slog.Logger

	mu     sync.Mutex
	token  string
	byRec  map[string]Edge // record id -> edge
	loaded bool
}

var (
	_ EdgeSource = (*MothershipProvider)(nil)
	_ Runner     = (*MothershipProvider)(nil)
)

var defaultMothershipClient = &http.Client{Transport: &http.Transport{}, Timeout: 15 * time.Second}

// Self implements [EdgeSource].
func (p *MothershipProvider) Self() string { return p.SelfID }

// Resolve implements [EdgeSource].
func (p *MothershipProvider) Resolve(remote string) (Edge, bool) {
	ref := strings.TrimPrefix(remote, "edge:")
	p.mu.Lock()
	defer p.mu.Unlock()
	if !strings.HasPrefix(ref, "@") {
		if e, ok := p.byRec[ref]; ok {
			return e, true
		}
	}
	id := strings.TrimPrefix(ref, "@")
	for _, e := range p.byRec {
		if e.ID == id {
			return e, true
		}
	}
	return Edge{}, false
}

// Edges implements [EdgeSource].
func (p *MothershipProvider) Edges() []Edge {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Edge, 0, len(p.byRec))
	for _, e := range p.byRec {
		out = append(out, e)
	}
	return out
}

// Loaded reports whether a read has succeeded.
func (p *MothershipProvider) Loaded() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loaded
}

// Run implements [Runner]: it refreshes now and every Refresh until ctx is done. Failures are logged and the last
// good list is kept.
func (p *MothershipProvider) Run(ctx context.Context) {
	every := p.Refresh
	if every <= 0 {
		every = 30 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := p.RefreshNow(ctx); err != nil && ctx.Err() == nil {
			p.log().Warn("edge: mothership edges", "url", p.URL, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type mothershipEdge struct {
	ID          string `json:"id"`
	EdgeID      string `json:"edge_id"`
	Region      string `json:"region"`
	InternalURL string `json:"internal_url"`
	Status      string `json:"status"`
	LastSeen    string `json:"last_seen"`
}

// RefreshNow reads the edge list once.
func (p *MothershipProvider) RefreshNow(ctx context.Context) error {
	recs, err := p.list(ctx)
	if errors.Is(err, errUnauthorized) {
		p.mu.Lock()
		p.token = ""
		p.mu.Unlock()
		recs, err = p.list(ctx)
	}
	if err != nil {
		return err
	}
	next := make(map[string]Edge, len(recs))
	for _, r := range recs {
		id := r.EdgeID
		if id == "" {
			id = r.ID
		}
		e := Edge{ID: id, InternalURL: r.InternalURL, Region: r.Region, Status: r.Status}
		if r.LastSeen != "" {
			e.LastSeen, _ = ParsePBTime(r.LastSeen)
		}
		next[r.ID] = e
	}
	p.mu.Lock()
	p.byRec, p.loaded = next, true
	p.mu.Unlock()
	return nil
}

var errUnauthorized = errors.New("unauthorized")

func (p *MothershipProvider) list(ctx context.Context) ([]mothershipEdge, error) {
	tok, err := p.login(ctx)
	if err != nil {
		return nil, err
	}
	coll := p.Collection
	if coll == "" {
		coll = "edges"
	}
	var out []mothershipEdge
	for page := 1; ; page++ {
		u := fmt.Sprintf("%s/api/collections/%s/records?perPage=500&page=%d&skipTotal=1", strings.TrimRight(p.URL, "/"), url.PathEscape(coll), page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", tok)
		var body struct {
			Items []mothershipEdge `json:"items"`
		}
		if err := p.do(req, &body); err != nil {
			return nil, err
		}
		out = append(out, body.Items...)
		if len(body.Items) < 500 {
			return out, nil
		}
	}
}

func (p *MothershipProvider) login(ctx context.Context) (string, error) {
	p.mu.Lock()
	tok := p.token
	p.mu.Unlock()
	if tok != "" {
		return tok, nil
	}
	b, _ := json.Marshal(map[string]string{"identity": p.Email, "password": p.Password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.URL, "/")+"/api/collections/_superusers/auth-with-password", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	var body struct {
		Token string `json:"token"`
	}
	if err := p.do(req, &body); err != nil {
		return "", fmt.Errorf("login: %w", err)
	}
	if body.Token == "" {
		return "", errors.New("login: no token")
	}
	p.mu.Lock()
	p.token = body.Token
	p.mu.Unlock()
	return body.Token, nil
}

func (p *MothershipProvider) do(req *http.Request, out any) error {
	c := p.Client
	if c == nil {
		c = defaultMothershipClient
	}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		_, _ = io.Copy(io.Discard, res.Body)
		return fmt.Errorf("%s %s: %d: %w", req.Method, req.URL.Path, res.StatusCode, errUnauthorized)
	}
	if res.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("%s %s: %d %s", req.Method, req.URL.Path, res.StatusCode, bytes.TrimSpace(msg))
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (p *MothershipProvider) log() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// ParsePBTime parses a PocketBase datetime ("2006-01-02 15:04:05.000Z") or RFC 3339.
func ParsePBTime(v string) (time.Time, error) {
	for _, f := range []string{"2006-01-02 15:04:05.000Z", "2006-01-02 15:04:05Z", time.RFC3339Nano} {
		if t, err := time.Parse(f, v); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad time %q", v)
}
