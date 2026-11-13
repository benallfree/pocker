package storage

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pockethost/pocketone/ext/pocker"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fixedSource []pocker.Instance

func (s fixedSource) Snapshot(context.Context) ([]pocker.Instance, error) { return s, nil }
func (s fixedSource) Watch(context.Context) (<-chan pocker.Change, error) { return nil, nil }

// startMinio runs a throwaway MinIO with buckets "shared" (the operator's) and "own" (an instance's own S3).
func startMinio(t *testing.T) *Bucket {
	t.Helper()
	bin, err := exec.LookPath("minio")
	if err != nil {
		t.Skip("minio not installed")
	}
	mc, err := exec.LookPath("mc")
	if err != nil {
		t.Skip("mc not installed")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	cmd := exec.Command(bin, "server", "--quiet", "--address", addr, "--console-address", "127.0.0.1:0", t.TempDir())
	cmd.Env = append(os.Environ(), "MINIO_ROOT_USER=storagetest", "MINIO_ROOT_PASSWORD=storagetest-secret")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	endpoint := "http://" + addr
	client := &http.Client{Transport: &http.Transport{}, Timeout: 2 * time.Second}
	for i := 0; ; i++ {
		if resp, err := client.Get(endpoint + "/minio/health/ready"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		if i > 50 {
			t.Fatal("minio did not start in 5s")
		}
		time.Sleep(100 * time.Millisecond)
	}
	cfgDir := t.TempDir()
	run := func(args ...string) {
		c := exec.Command(mc, append([]string{"--config-dir", cfgDir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("mc %v: %v\n%s", args, err, out)
		}
	}
	run("alias", "set", "t", endpoint, "storagetest", "storagetest-secret")
	run("mb", "t/shared")
	run("mb", "t/own")
	return &Bucket{Endpoint: endpoint, Bucket: "shared", Region: "us-east-1", AccessKey: "storagetest", Secret: "storagetest-secret", ForcePathStyle: true, Prefix: "eu/"}
}

// raw opens a bucket without any fence, to check where things landed.
func raw(t *testing.T, b *Bucket, name string) *filesystem.System {
	t.Helper()
	fsys, err := filesystem.NewS3(name, b.Region, b.Endpoint, b.AccessKey, b.Secret, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fsys.Close() })
	return fsys
}

func exists(t *testing.T, fsys *filesystem.System, key string) bool {
	t.Helper()
	ok, err := fsys.Exists(key)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func pngBytes(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for x := 0; x < 40; x++ {
		for y := 0; y < 40; y++ {
			img.Set(x, y, color.RGBA{uint8(x * 6), uint8(y * 6), 90, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDefaultBucketUnderPocker(t *testing.T) {
	b := startMinio(t)
	shared := raw(t, b, "shared")
	pbRoot := t.TempDir()

	// a file on instance a's local disk from before the plugin
	oldLocal := filepath.Join(pbRoot, "a", "pb_data", "storage", "x", "old.txt")
	if err := os.MkdirAll(filepath.Dir(oldLocal), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldLocal, []byte("from before"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := New(Config{Bucket: b, Logger: quiet})
	insts := fixedSource{
		{ID: "a", Hosts: []string{"a.test"}},
		{ID: "b", Hosts: []string{"b.test"}},
		{ID: "c", Hosts: []string{"c.test"}, Plugins: map[string]any{"storage": map[string]any{"enabled": false}}},
	}
	var mu sync.Mutex
	apps := map[string]core.App{}
	m := pocker.NewManager(insts, pocker.ManagerOptions{
		PBRoot: pbRoot, Logger: quiet, Init: p.InitApp,
		Setup: func(inst pocker.Instance, app core.App) error {
			mu.Lock()
			apps[inst.ID] = app
			mu.Unlock()
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	defer m.Shutdown()
	for !m.Ready() {
		time.Sleep(10 * time.Millisecond)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := m.StartInstance(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	appA, appB, appC := apps["a"], apps["b"], apps["c"]

	fsA, err := appA.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	defer fsA.Close()

	// new uploads go to the bucket, under the instance's folder, not to local disk
	if err := fsA.Upload([]byte("hello"), "c1/r1/new.txt"); err != nil {
		t.Fatal(err)
	}
	if !exists(t, shared, "eu/instances/a/storage/c1/r1/new.txt") {
		t.Fatal("upload not in the bucket under eu/instances/a/storage/")
	}
	if _, err := os.Stat(filepath.Join(pbRoot, "a", "pb_data", "storage", "c1", "r1", "new.txt")); err == nil {
		t.Fatal("upload also written to local disk")
	}

	// a file left on local disk is not served or moved: the plugin is for fresh instances
	if ok, _ := fsA.Exists("x/old.txt"); ok {
		t.Fatal("local file visible through the default bucket")
	}
	if _, err := os.Stat(oldLocal); err != nil {
		t.Fatal("local file touched")
	}

	// fenced: b sees none of a's files and can't reach them by key
	fsB, err := appB.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	defer fsB.Close()
	if l, err := fsB.List(""); err != nil || len(l) != 0 {
		t.Fatalf("b lists %d objects (%v), want 0", len(l), err)
	}
	if exists(t, fsB, "../a/storage/c1/r1/new.txt") || exists(t, fsB, "c1/r1/new.txt") {
		t.Fatal("b reached a's file")
	}
	if l, err := fsA.List(""); err != nil || len(l) != 1 {
		t.Fatalf("a lists %d objects (%v), want 1", len(l), err)
	}

	// deletes go through to the bucket
	if err := fsA.Delete("c1/r1/new.txt"); err != nil {
		t.Fatal(err)
	}
	if exists(t, shared, "eu/instances/a/storage/c1/r1/new.txt") {
		t.Fatal("delete did not reach the bucket")
	}

	// opted out: local disk as before
	fsC, err := appC.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	defer fsC.Close()
	if err := fsC.Upload([]byte("c"), "k.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pbRoot, "c", "pb_data", "storage", "k.txt")); err != nil {
		t.Fatal("opted-out instance didn't write to local disk")
	}

	// settings still say S3 off
	if appA.Settings().S3.Enabled || appA.Settings().Backups.S3.Enabled {
		t.Fatal("plugin changed the app's S3 settings")
	}

	// backups go to the bucket's backups folder
	if err := appA.CreateBackup(ctx, "z.zip"); err != nil {
		t.Fatal(err)
	}
	if !exists(t, shared, "eu/instances/a/backups/z.zip") {
		t.Fatal("backup zip not in the bucket")
	}
	if _, err := os.Stat(filepath.Join(pbRoot, "a", "pb_data", "backups", "z.zip")); err == nil {
		t.Fatal("backup zip also on local disk")
	}

	// record file + thumb over HTTP, through the normal files API
	col := core.NewBaseCollection("docs")
	col.ViewRule = new(string)
	col.Fields.Add(&core.FileField{Name: "img", MaxSelect: 1, MaxSize: 1 << 20, Thumbs: []string{"10x10"}})
	if err := appA.Save(col); err != nil {
		t.Fatal(err)
	}
	f, err := filesystem.NewFileFromBytes(pngBytes(t), "pic.png")
	if err != nil {
		t.Fatal(err)
	}
	rec := core.NewRecord(col)
	rec.Set("img", f)
	if err := appA.Save(rec); err != nil {
		t.Fatal(err)
	}
	name := rec.GetString("img")
	key := col.Id + "/" + rec.Id + "/" + name
	if !exists(t, shared, "eu/instances/a/storage/"+key) {
		t.Fatal("record file not in the bucket")
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		m.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://a.test"+path, nil))
		return w
	}
	if w := get("/api/files/docs/" + rec.Id + "/" + name); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), pngBytes(t)) {
		t.Fatalf("file download: %d (%d bytes)", w.Code, w.Body.Len())
	}
	if w := get("/api/files/docs/" + rec.Id + "/" + name + "?thumb=10x10"); w.Code != 200 || w.Body.Len() == 0 {
		t.Fatalf("thumb download: %d", w.Code)
	}
	if !exists(t, shared, "eu/instances/a/storage/"+col.Id+"/"+rec.Id+"/thumbs_"+name+"/10x10_"+name) {
		t.Fatal("thumb not stored in the bucket")
	}

	// an instance turns on its own S3: PocketBase's normal code, its own bucket, no prefix (on b: a settings save
	// after HTTP requests trips an upstream race between ReloadSettings and the request logger)
	s, err := appB.Settings().Clone()
	if err != nil {
		t.Fatal(err)
	}
	s.S3.Enabled, s.S3.Bucket, s.S3.Region, s.S3.Endpoint = true, "own", b.Region, b.Endpoint
	s.S3.AccessKey, s.S3.Secret, s.S3.ForcePathStyle = b.AccessKey, b.Secret, true
	if err := appB.Save(s); err != nil {
		t.Fatal(err)
	}
	fsOwn, err := appB.NewFilesystem()
	if err != nil {
		t.Fatal(err)
	}
	defer fsOwn.Close()
	if err := fsOwn.Upload([]byte("mine"), "mine.txt"); err != nil {
		t.Fatal(err)
	}
	if !exists(t, raw(t, b, "own"), "mine.txt") || exists(t, shared, "eu/instances/b/storage/mine.txt") {
		t.Fatal("with its own S3 on, the upload didn't go to the instance's bucket")
	}

}
