package storage

import (
	"context"
	"errors"
	"io"
	"sort"
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

// fallbackDriver serves from the bucket first and from the instance's local folder for files that haven't moved
// yet. New files go to the bucket only. A local hit calls onLocalHit (the plugin moves the file up in the
// background with its own connections, since PocketBase closes this filesystem right after use).
type fallbackDriver struct {
	primary    blob.Driver // the fenced bucket
	local      blob.Driver // pb_data/storage or pb_data/backups
	onLocalHit func(key string)
}

func (d *fallbackDriver) notFound(drv blob.Driver, err error) bool {
	return err != nil && errors.Is(drv.NormalizeError(err), blob.ErrNotFound)
}

func (d *fallbackDriver) NormalizeError(err error) error {
	if n := d.primary.NormalizeError(err); errors.Is(n, blob.ErrNotFound) {
		return n
	}
	return d.local.NormalizeError(err)
}

func (d *fallbackDriver) Attributes(ctx context.Context, key string) (*blob.Attributes, error) {
	a, err := d.primary.Attributes(ctx, key)
	if d.notFound(d.primary, err) {
		return d.local.Attributes(ctx, key)
	}
	return a, err
}

func (d *fallbackDriver) NewRangeReader(ctx context.Context, key string, offset, length int64) (blob.DriverReader, error) {
	r, err := d.primary.NewRangeReader(ctx, key, offset, length)
	if !d.notFound(d.primary, err) {
		return r, err
	}
	r, err = d.local.NewRangeReader(ctx, key, offset, length)
	if err == nil && d.onLocalHit != nil {
		d.onLocalHit(key)
	}
	return r, err
}

func copyBetween(ctx context.Context, from, to blob.Driver, srcKey, dstKey string) error {
	r, err := from.NewRangeReader(ctx, srcKey, 0, -1)
	if err != nil {
		return err
	}
	defer r.Close()
	ct := r.Attributes().ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w, err := to.NewTypedWriter(cctx, dstKey, ct, &blob.WriterOptions{})
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, r); err != nil {
		cancel() // abort the upload
		_ = w.Close()
		return err
	}
	return w.Close()
}

func (d *fallbackDriver) NewTypedWriter(ctx context.Context, key, contentType string, opts *blob.WriterOptions) (blob.DriverWriter, error) {
	return d.primary.NewTypedWriter(ctx, key, contentType, opts)
}

func (d *fallbackDriver) Copy(ctx context.Context, dstKey, srcKey string) error {
	err := d.primary.Copy(ctx, dstKey, srcKey)
	if !d.notFound(d.primary, err) {
		return err
	}
	return copyBetween(ctx, d.local, d.primary, srcKey, dstKey)
}

// Delete removes the key from both places; not found only if it was in neither.
func (d *fallbackDriver) Delete(ctx context.Context, key string) error {
	errP := d.primary.Delete(ctx, key)
	errL := d.local.Delete(ctx, key)
	pMissing, lMissing := errP == nil || d.notFound(d.primary, errP), errL == nil || d.notFound(d.local, errL)
	switch {
	case errP != nil && !pMissing:
		return errP
	case errL != nil && !lMissing:
		return errL
	case errP != nil && errL != nil: // missing in both
		return errP
	}
	return nil
}

// ListPaged merges both listings (bucket wins on the same key), in key order, as one page. PocketBase lists only to
// delete a record's or collection's files and to check for an empty folder, so one page is fine there.
func (d *fallbackDriver) ListPaged(ctx context.Context, opts *blob.ListOptions) (*blob.ListPage, error) {
	all := func(drv blob.Driver) ([]*blob.ListObject, error) {
		var out []*blob.ListObject
		o := *opts
		o.PageToken = nil
		for {
			p, err := drv.ListPaged(ctx, &o)
			if err != nil {
				return nil, err
			}
			out = append(out, p.Objects...)
			if len(p.NextPageToken) == 0 {
				return out, nil
			}
			o.PageToken = p.NextPageToken
		}
	}
	if len(opts.PageToken) > 0 { // everything was returned in the first page
		return &blob.ListPage{}, nil
	}
	p, err := all(d.primary)
	if err != nil {
		return nil, err
	}
	l, err := all(d.local)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(p))
	for _, o := range p {
		seen[o.Key] = true
	}
	for _, o := range l {
		if !seen[o.Key] {
			p = append(p, o)
		}
	}
	sort.Slice(p, func(i, j int) bool { return p[i].Key < p[j].Key })
	return &blob.ListPage{Objects: p}, nil
}

func (d *fallbackDriver) Close() error {
	return errors.Join(d.primary.Close(), d.local.Close())
}

type blobDriver = blob.Driver

// moveFile copies key from local to the bucket, then removes the local copy (an open reader keeps reading it).
func moveFile(local, primary blob.Driver, key string) error {
	ctx := context.Background()
	if err := copyBetween(ctx, local, primary, key, key); err != nil {
		return err
	}
	if err := local.Delete(ctx, key); err != nil && !errors.Is(local.NormalizeError(err), blob.ErrNotFound) {
		return err
	}
	return nil
}
