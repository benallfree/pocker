package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem/blob"
)

// prefixDriver fences a driver to the keys under prefix: the app sees "a/b.png", the bucket stores
// "<prefix>a/b.png". Nothing outside the prefix can be read, listed, written or deleted.
type prefixDriver struct {
	blob.Driver
	prefix string // ends with "/"
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

// SignedURL signs the prefixed key when the wrapped driver can sign (S3), so code that signs through the filesystem
// (ext/s3redirect) sees the fenced bucket like any other S3 mount.
func (d prefixDriver) SignedURL(ctx context.Context, key string, opts *blob.SignedURLOptions) (string, error) {
	s, ok := d.Driver.(blob.URLSigner)
	if !ok {
		return "", fmt.Errorf("signed URLs: %w", errors.ErrUnsupported)
	}
	return s.SignedURL(ctx, d.k(key), opts)
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
