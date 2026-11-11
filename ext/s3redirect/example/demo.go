package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3bolt"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
)

// The demo bucket's fixed credentials. Local only: the listener is bound to the address you give (loopback by default).
const (
	demoBucket = "demo"
	demoRegion = "us-east-1"
	demoKey    = "demo-access-key"
	demoSecret = "demo-secret-key"
	demoColl   = "demo_files"
)

// registerDemo starts an in-process S3 bucket on addr when the app bootstraps, points the app's Files storage at it
// (unless the app already uses another S3), and seeds a demo record once the app serves.
func registerDemo(app core.App, addr string) {
	endpoint := "http://" + addr

	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		s := e.App.Settings()
		if s.S3.Enabled && strings.TrimRight(s.S3.Endpoint, "/") != endpoint {
			e.App.Logger().Info("s3redirect demo: the app already uses its own S3, demo bucket not started", "endpoint", s.S3.Endpoint)
			return nil
		}
		if err := startBucket(e.App, addr); err != nil {
			return fmt.Errorf("s3redirect demo bucket: %w", err)
		}
		s.S3 = core.S3Config{Enabled: true, Endpoint: endpoint, Bucket: demoBucket, Region: demoRegion,
			AccessKey: demoKey, Secret: demoSecret, ForcePathStyle: true}
		return e.App.Save(s)
	})

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		if err := se.Next(); err != nil {
			return err
		}
		if strings.TrimRight(se.App.Settings().S3.Endpoint, "/") != endpoint {
			return nil
		}
		rec, err := seed(se.App)
		if err != nil {
			return fmt.Errorf("s3redirect demo seed: %w", err)
		}
		base := "http://" + se.Server.Addr
		files := base + "/api/files/" + rec.Collection().Name + "/" + rec.Id + "/"
		fmt.Printf(`
s3redirect demo: files are in the in-process bucket at %s (bucket %q).
  public image:  %s
  thumbnail:     %s?thumb=100x100
  download:      %s?download=1
  protected doc: %s  (404 without a file token: log in as user@example.com, POST /api/files/token, add ?token=...)
Try: curl -sI '%s'   then follow the Location.

`, endpoint, demoBucket, files+rec.GetString("image"), files+rec.GetString("image"), files+rec.GetString("image"),
			files+rec.GetString("doc"), files+rec.GetString("image"))
		return nil
	})
}

// startBucket runs gofakes3 on addr with its objects in pb_data/demo-s3.bolt, behind a check that only lets
// through requests the app's own S3 client makes (Authorization header) and presigned GETs whose signature and
// expiry are valid.
func startBucket(app core.App, addr string) error {
	backend, err := s3bolt.NewFile(filepath.Join(app.DataDir(), "demo-s3.bolt"))
	if err != nil {
		return err
	}
	_ = backend.CreateBucket(demoBucket) // exists after the first run
	faker := gofakes3.New(backend, gofakes3.WithAutoBucket(true))

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: guard(faker.Server()), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("s3redirect demo bucket: %v", err)
		}
	}()
	app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		return e.Next()
	})
	return nil
}

// guard stands in for a real bucket's access control, which gofakes3 doesn't have.
func guard(next http.Handler) http.Handler {
	creds := aws.Credentials{AccessKeyID: demoKey, SecretAccessKey: demoSecret}
	signer := v4.NewSigner()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("X-Amz-Signature") != "":
			if err := checkPresigned(r, signer, creds); err != nil {
				log.Printf("demo bucket: 403 %s (%v)", r.URL.Path, err)
				http.Error(w, "AccessDenied: "+err.Error(), http.StatusForbidden)
				return
			}
			log.Printf("demo bucket: presigned GET %s, signature valid (checked with the AWS SDK)", r.URL.Path)
		case strings.Contains(r.Header.Get("Authorization"), "Credential="+demoKey+"/"):
			// the app's own S3 client (uploads, thumbs, deletes); gofakes3 doesn't verify header signatures
		default:
			log.Printf("demo bucket: 403 %s (unsigned)", r.URL.Path)
			http.Error(w, "AccessDenied: unsigned request", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// checkPresigned re-signs the request with the AWS SDK and compares, then checks the expiry.
func checkPresigned(r *http.Request, signer *v4.Signer, creds aws.Credentials) error {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return errors.New("presigned URLs are for GET")
	}
	q := r.URL.Query()
	signedAt, err := time.Parse("20060102T150405Z", q.Get("X-Amz-Date"))
	if err != nil {
		return errors.New("bad X-Amz-Date")
	}
	expires, err := strconv.Atoi(q.Get("X-Amz-Expires"))
	if err != nil || expires <= 0 {
		return errors.New("bad X-Amz-Expires")
	}
	if time.Now().After(signedAt.Add(time.Duration(expires) * time.Second)) {
		return errors.New("expired")
	}

	// the URL without its auth parameters, as it was before signing (X-Amz-Expires stays: the SDK reads it)
	got := q.Get("X-Amz-Signature")
	for _, k := range []string{"X-Amz-Signature", "X-Amz-Algorithm", "X-Amz-Credential", "X-Amz-Date", "X-Amz-SignedHeaders"} {
		q.Del(k)
	}
	u := url.URL{Scheme: "http", Host: r.Host, Path: r.URL.Path, RawPath: r.URL.RawPath, RawQuery: q.Encode()}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	signed, _, err := signer.PresignHTTP(r.Context(), creds, req, "UNSIGNED-PAYLOAD", "s3", demoRegion, signedAt)
	if err != nil {
		return err
	}
	su, err := url.Parse(signed)
	if err != nil {
		return err
	}
	if want := su.Query().Get("X-Amz-Signature"); want != got {
		return errors.New("signature mismatch")
	}
	return nil
}

// seed makes the demo collection and one record (uploaded through PocketBase, so into the bucket) if missing.
func seed(app core.App) (*core.Record, error) {
	col, err := app.FindCollectionByNameOrId(demoColl)
	if err != nil {
		col = core.NewBaseCollection(demoColl)
		col.ListRule = ptr(`@request.auth.id != ""`)
		col.ViewRule = ptr(`@request.auth.id != ""`)
		col.Fields.Add(&core.FileField{Name: "image", MaxSelect: 1, MaxSize: 5 << 20, Thumbs: []string{"100x100"}})
		col.Fields.Add(&core.FileField{Name: "doc", MaxSelect: 1, MaxSize: 5 << 20, Protected: true})
		if err := app.Save(col); err != nil {
			return nil, err
		}
	}
	if recs, err := app.FindRecordsByFilter(col, "", "-created", 1, 0); err == nil && len(recs) > 0 {
		return recs[0], nil
	}

	img, err := demoPNG()
	if err != nil {
		return nil, err
	}
	imgFile, err := filesystem.NewFileFromBytes(img, "gradient.png")
	if err != nil {
		return nil, err
	}
	docFile, err := filesystem.NewFileFromBytes([]byte("Only a logged-in user with a file token can read this.\n"), "secret.txt")
	if err != nil {
		return nil, err
	}
	rec := core.NewRecord(col)
	rec.Set("image", imgFile)
	rec.Set("doc", docFile)
	return rec, app.Save(rec)
}

func demoPNG() ([]byte, error) {
	m := image.NewRGBA(image.Rect(0, 0, 320, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 320; x++ {
			m.Set(x, y, color.RGBA{uint8(x * 255 / 320), uint8(y * 255 / 200), 180, 255})
		}
	}
	var b bytes.Buffer
	err := png.Encode(&b, m)
	return b.Bytes(), err
}

func ptr[T any](v T) *T { return &v }
