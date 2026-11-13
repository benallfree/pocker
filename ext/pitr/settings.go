package pitr

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/dbx"
	"ganigeorgiev.com/pocketbase/pocketbase/core"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/security"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/types"
)

// Kind is the filesystem kind PITR asks the OnFilesystemCreate handlers for: the operator's fall-through storage
// (ext/storage mount `litestream`) serves it when mounted.
const Kind = "litestream"

// DefaultS3Prefix is where the history goes in an app's own bucket when the form leaves the prefix empty.
const DefaultS3Prefix = "litestream"

// settingsParam is the app's _params row holding PITR's settings (like PocketBase's own "settings" row).
const settingsParam = "pitr_settings"

// Settings are one app's PITR settings, edited by its superusers (Settings → Backups → Point-in-time restore).
type Settings struct {
	S3 S3Settings `json:"s3"`
}

// S3Settings point the app's history at its own S3-compatible bucket, like Settings → Backups → S3 does for zips.
type S3Settings struct {
	Enabled        bool   `json:"enabled"`
	Bucket         string `json:"bucket"`
	Region         string `json:"region"`
	Endpoint       string `json:"endpoint"`
	AccessKey      string `json:"accessKey"`
	Secret         string `json:"secret,omitempty"` // never returned by the API
	ForcePathStyle bool   `json:"forcePathStyle"`
	Prefix         string `json:"prefix"` // key prefix in the bucket (default "litestream")
}

func (s S3Settings) prefix() string {
	if p := strings.Trim(s.Prefix, "/"); p != "" {
		return p
	}
	return DefaultS3Prefix
}

func (s S3Settings) validate() error {
	if !s.Enabled {
		return nil
	}
	var missing []string
	for _, f := range []struct{ name, v string }{{"endpoint", s.Endpoint}, {"bucket", s.Bucket}, {"region", s.Region}, {"access key", s.AccessKey}, {"secret", s.Secret}} {
		if strings.TrimSpace(f.v) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing S3 %s", strings.Join(missing, ", "))
	}
	return nil
}

func (s S3Settings) open() (*filesystem.System, error) {
	return filesystem.NewS3(s.Bucket, s.Region, s.Endpoint, s.AccessKey, s.Secret, s.ForcePathStyle)
}

// stored is the row's JSON: the secret is encrypted with the app's encryption key when it runs with one.
type stored struct {
	Settings
	SecretEnc string `json:"secretEnc,omitempty"`
}

func encryptionKey(app core.App) string {
	if env := app.EncryptionEnv(); env != "" {
		return os.Getenv(env)
	}
	return ""
}

// LoadSettings returns the app's PITR settings (zero value when never saved), secret included (decrypted).
func LoadSettings(app core.App) (Settings, error) {
	var raw string
	err := app.DB().NewQuery("SELECT value FROM {{_params}} WHERE id = {:id}").Bind(dbx.Params{"id": settingsParam}).Row(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var st stored
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return Settings{}, fmt.Errorf("pitr: settings: %w", err)
	}
	if st.SecretEnc != "" {
		key := encryptionKey(app)
		if key == "" {
			return Settings{}, errors.New("pitr: settings: the S3 secret is encrypted but the app has no encryption key")
		}
		plain, err := security.Decrypt(st.SecretEnc, key)
		if err != nil {
			return Settings{}, fmt.Errorf("pitr: settings: decrypting the S3 secret: %w", err)
		}
		st.S3.Secret = string(plain)
	}
	return st.Settings, nil
}

// SaveSettings stores the app's PITR settings (the secret encrypted when the app has an encryption key).
func SaveSettings(app core.App, s Settings) error {
	if err := s.S3.validate(); err != nil {
		return err
	}
	st := stored{Settings: s}
	if key := encryptionKey(app); key != "" && s.S3.Secret != "" {
		enc, err := security.Encrypt([]byte(s.S3.Secret), key)
		if err != nil {
			return err
		}
		st.SecretEnc, st.S3.Secret = enc, ""
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	now := types.NowDateTime().String()
	_, err = app.DB().NewQuery(`INSERT INTO {{_params}} (id, value, created, updated) VALUES ({:id}, {:value}, {:now}, {:now})
		ON CONFLICT(id) DO UPDATE SET value = excluded.value, updated = excluded.updated`).
		Bind(dbx.Params{"id": settingsParam, "value": string(b), "now": now}).Execute()
	return err
}

// publicSettings is what the API returns: no secret, only whether one is set.
type publicSettings struct {
	S3 struct {
		S3Settings
		SecretSet bool `json:"secretSet"`
	} `json:"s3"`
}

func toPublic(s Settings) publicSettings {
	var p publicSettings
	p.S3.S3Settings = s.S3
	p.S3.SecretSet = s.S3.Secret != ""
	p.S3.Secret = ""
	return p
}

// mergeSecret keeps the stored secret when an update leaves it empty (the form never receives it).
func mergeSecret(next, prev Settings) Settings {
	if next.S3.Secret == "" {
		next.S3.Secret = prev.S3.Secret
	}
	return next
}

// testS3 checks that the bucket accepts a write and a delete under the prefix.
func testS3(s S3Settings) error {
	s.Enabled = true
	if err := s.validate(); err != nil {
		return err
	}
	fsys, err := s.open()
	if err != nil {
		return err
	}
	defer fsys.Close()
	key := s.prefix() + "/.pitr-test-" + time.Now().UTC().Format("20060102T150405.000000000")
	if err := fsys.Upload([]byte("ok"), key); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := fsys.Delete(key); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}
