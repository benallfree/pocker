package storage

import (
	"context"
	"net/url"
	"strings"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem/blob"
	"github.com/pockethost/pocketbase/ext/s3redirect/sigv4"
)

// prefixDriver fences a driver to the keys under prefix: the app sees "a/b.png", the bucket stores
// "<prefix>a/b.png". Nothing outside the prefix can be read, listed, written or deleted.
type prefixDriver struct {
	blob.Driver
	prefix string       // ends with "/"
	sign   sigv4.Config // the bucket, for presigned downloads
}

func (d prefixDriver) k(key string) string { return d.prefix + key }

func (d prefixDriver) Attributes(ctx context.Context, key string) (*blob.Attributes, error) {
	return d.Driver.Attributes(ctx, d.k(key))
}

func (d prefixDriver) NewRangeReader(ctx context.Context, key string, offset, length int64) (blob.DriverReader, error) {
	return d.Driver.NewRangeReader(ctx, d.k(key), offset, length)
}

func (d prefixDriver) NewTypedWriter(ctx context.Context, key, contentType string, opts *blob.WriterOptions) (blob.DriverWriter, error) {
	return d.Driver.NewTypedWriter(ctx, d.k(key), contentType, opts)
}

func (d prefixDriver) Copy(ctx context.Context, dstKey, srcKey string) error {
	return d.Driver.Copy(ctx, d.k(dstKey), d.k(srcKey))
}

func (d prefixDriver) Delete(ctx context.Context, key string) error {
	return d.Driver.Delete(ctx, d.k(key))
}

// SignedURL signs the prefixed key with the bucket's own credentials (ext/s3redirect's Signer), so downloads from the
// fenced bucket redirect like any other S3 mount.
func (d prefixDriver) SignedURL(key string, ttl time.Duration, now time.Time, overrides url.Values) (string, error) {
	return d.sign.PresignGet(d.k(key), ttl, now, overrides)
}

func (d prefixDriver) ListPaged(ctx context.Context, opts *blob.ListOptions) (*blob.ListPage, error) {
	o := *opts
	o.Prefix = d.prefix + opts.Prefix
	page, err := d.Driver.ListPaged(ctx, &o)
	if err != nil {
		return nil, err
	}
	for _, obj := range page.Objects {
		obj.Key = strings.TrimPrefix(obj.Key, d.prefix)
	}
	return page, nil
}
