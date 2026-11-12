package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// engine is a minimal Docker Engine API client over the daemon's unix socket (no CLI, no SDK).
type engine struct {
	http *http.Client

	mu      sync.Mutex
	version string // "/v1.47": the daemon's API version, capped at maxAPI ("" = not negotiated yet)
}

// maxAPI is the newest Engine API version this client speaks. The daemon's own version is used when older; older
// versions than the daemon's are avoided because the daemon then applies compatibility behavior (e.g. a bind-mounted
// binary failing to exec under v1.41 on Docker 29).
const (
	maxAPI = "1.47"
	minAPI = "1.41"
)

// prefix negotiates the API version once (GET /version) and returns the path prefix.
func (e *engine) prefix(ctx context.Context) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.version != "" {
		return e.version
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/version", nil)
	if err != nil {
		return "/v" + minAPI
	}
	res, err := e.http.Do(req)
	if err != nil {
		return "/v" + minAPI // daemon not reachable yet: negotiate again next call
	}
	defer res.Body.Close()
	var v struct {
		APIVersion string `json:"ApiVersion"`
	}
	e.version = "/v" + minAPI
	if json.NewDecoder(res.Body).Decode(&v) == nil && v.APIVersion != "" {
		e.version = "/v" + minVersion(v.APIVersion, maxAPI)
	}
	return e.version
}

// minVersion compares "major.minor" API versions.
func minVersion(a, b string) string {
	var am, an, bm, bn int
	fmt.Sscanf(a, "%d.%d", &am, &an)
	fmt.Sscanf(b, "%d.%d", &bm, &bn)
	if am < bm || (am == bm && an < bn) {
		return a
	}
	return b
}

// DefaultSocket is the Docker socket: $DOCKER_HOST when it is unix://..., else /var/run/docker.sock.
func DefaultSocket() string {
	if h := os.Getenv("DOCKER_HOST"); strings.HasPrefix(h, "unix://") {
		return strings.TrimPrefix(h, "unix://")
	}
	return "/var/run/docker.sock"
}

func newEngine(socket string) *engine {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
		},
		MaxIdleConnsPerHost: 16,
	}
	return &engine{http: &http.Client{Transport: tr}}
}

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("docker: %d %s", e.Status, e.Message) }

func isNotFound(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == http.StatusNotFound
}

func isConflict(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.Status == http.StatusConflict
}

// call sends one request (bounded by timeout unless it is 0) and decodes a JSON answer into out (if non-nil).
func (e *engine) call(ctx context.Context, timeout time.Duration, method, path string, in, out any) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+e.prefix(ctx)+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := e.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var m struct {
			Message string `json:"message"`
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		_ = json.Unmarshal(b, &m)
		if m.Message == "" {
			m.Message = strings.TrimSpace(string(b))
		}
		return &apiError{Status: res.StatusCode, Message: m.Message}
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	return nil
}

// containerConfig is the subset of the create body this package uses.
type containerConfig struct {
	Image        string              `json:"Image"`
	Cmd          []string            `json:"Cmd"`
	Env          []string            `json:"Env"`
	WorkingDir   string              `json:"WorkingDir,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	Tty          bool                `json:"Tty"`
	HostConfig   hostConfig          `json:"HostConfig"`
}

type hostConfig struct {
	Binds          []string                 `json:"Binds,omitempty"`
	PortBindings   map[string][]portBinding `json:"PortBindings,omitempty"`
	Init           bool                     `json:"Init"`
	AutoRemove     bool                     `json:"AutoRemove"`
	Ulimits        []ulimit                 `json:"Ulimits,omitempty"`
	Memory         int64                    `json:"Memory,omitempty"`
	NanoCPUs       int64                    `json:"NanoCpus,omitempty"`
	PidsLimit      int64                    `json:"PidsLimit,omitempty"`
	CapDrop        []string                 `json:"CapDrop,omitempty"`
	SecurityOpt    []string                 `json:"SecurityOpt,omitempty"`
	NetworkMode    string                   `json:"NetworkMode,omitempty"`
	ReadonlyRootfs bool                     `json:"ReadonlyRootfs,omitempty"`
}

type portBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type ulimit struct {
	Name string `json:"Name"`
	Soft int64  `json:"Soft"`
	Hard int64  `json:"Hard"`
}

type inspect struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Image  string `json:"Image"`
	State  struct {
		Running   bool   `json:"Running"`
		StartedAt string `json:"StartedAt"`
	} `json:"State"`
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	Mounts []struct {
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Ports map[string][]portBinding `json:"Ports"`
	} `json:"NetworkSettings"`
}

// hostPort is the host port 8090/tcp is published on (0 when none).
func (i inspect) hostPort() int {
	for _, b := range i.NetworkSettings.Ports["8090/tcp"] {
		var p int
		if _, err := fmt.Sscan(b.HostPort, &p); err == nil && p > 0 {
			return p
		}
	}
	return 0
}

type summary struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
	State  string            `json:"State"`
}

func (e *engine) create(ctx context.Context, name string, cfg containerConfig) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	err := e.call(ctx, 30*time.Second, http.MethodPost, "/containers/create?name="+url.QueryEscape(name), cfg, &out)
	return out.ID, err
}

func (e *engine) start(ctx context.Context, id string) error {
	return e.call(ctx, 30*time.Second, http.MethodPost, "/containers/"+id+"/start", nil, nil)
}

func (e *engine) inspect(ctx context.Context, id string) (inspect, error) {
	var out inspect
	err := e.call(ctx, 15*time.Second, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, &out)
	return out, err
}

// stop sends SIGINT and waits up to grace before Docker kills it (like the Node daemon).
func (e *engine) stop(ctx context.Context, id string, grace time.Duration) error {
	q := fmt.Sprintf("?signal=SIGINT&t=%d", int(grace/time.Second))
	err := e.call(ctx, grace+15*time.Second, http.MethodPost, "/containers/"+url.PathEscape(id)+"/stop"+q, nil, nil)
	if isNotFound(err) {
		return nil
	}
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusNotModified {
		return nil
	}
	return err
}

func (e *engine) remove(ctx context.Context, id string) error {
	err := e.call(ctx, 30*time.Second, http.MethodDelete, "/containers/"+url.PathEscape(id)+"?force=1", nil, nil)
	if isNotFound(err) || isConflict(err) { // gone, or removal already in progress (AutoRemove)
		return nil
	}
	return err
}

func (e *engine) list(ctx context.Context, filters map[string][]string) ([]summary, error) {
	b, _ := json.Marshal(filters)
	var out []summary
	err := e.call(ctx, 30*time.Second, http.MethodGet, "/containers/json?filters="+url.QueryEscape(string(b)), nil, &out)
	return out, err
}

func (e *engine) imageExists(ctx context.Context, ref string) (bool, error) {
	err := e.call(ctx, 15*time.Second, http.MethodGet, "/images/"+ref+"/json", nil, nil)
	if isNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// pull fetches an image (blocking until the pull stream ends, at most 10 minutes).
func (e *engine) pull(ctx context.Context, ref string) error {
	name, tag := ref, "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		name, tag = ref[:i], ref[i+1:]
	}
	return e.call(ctx, 10*time.Minute, http.MethodPost, "/images/create?fromImage="+url.QueryEscape(name)+"&tag="+url.QueryEscape(tag), nil, nil)
}

// logs follows a container's stdout/stderr (non-TTY multiplexed stream) and calls line for each line until the
// stream ends.
func (e *engine) logs(ctx context.Context, id string, line func(stream int, text string)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+e.prefix(ctx)+"/containers/"+id+"/logs?follow=1&stdout=1&stderr=1", nil)
	if err != nil {
		return err
	}
	res, err := e.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return &apiError{Status: res.StatusCode, Message: "logs"}
	}
	r := bufio.NewReader(res.Body)
	var hdr [8]byte
	partial := map[int]string{}
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil
		}
		stream := int(hdr[0])
		n := binary.BigEndian.Uint32(hdr[4:])
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil
		}
		text := partial[stream] + string(buf)
		for {
			i := strings.IndexByte(text, '\n')
			if i < 0 {
				break
			}
			line(stream, strings.TrimRight(text[:i], "\r"))
			text = text[i+1:]
		}
		partial[stream] = text
	}
}
