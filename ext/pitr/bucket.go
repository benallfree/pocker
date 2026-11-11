package pitr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/benbjohnson/litestream"
	"github.com/superfly/ltx"

	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem"
	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem/blob"
)

// metaTimestamp is where each LTX file's own timestamp is kept (as Litestream's S3 client does), so restoring to a
// time doesn't depend on when the object was uploaded.
const metaTimestamp = "litestream-timestamp"

// bucketClient is a Litestream replica client on PocketBase's own S3 filesystem (no AWS SDK). Every key is under
// root, so one app can't read or list another's history.
type bucketClient struct {
	fs   *filesystem.System
	root string // e.g. "edge/instances/abc/litestream"
}

var _ litestream.ReplicaClient = (*bucketClient)(nil)

func newBucketClient(fs *filesystem.System, root string) *bucketClient {
	return &bucketClient{fs: fs, root: strings.Trim(root, "/")}
}

func (c *bucketClient) Type() string                   { return "s3" }
func (c *bucketClient) Init(ctx context.Context) error { return nil }
func (c *bucketClient) SetLogger(*slog.Logger)         {}

func (c *bucketClient) levelDir(level int) string { return litestream.LTXLevelDir(c.root, level) + "/" }

func (c *bucketClient) key(level int, minTXID, maxTXID ltx.TXID) string {
	return litestream.LTXFilePath(c.root, level, minTXID, maxTXID)
}

func (c *bucketClient) LTXFiles(ctx context.Context, level int, seek ltx.TXID, useMetadata bool) (ltx.FileIterator, error) {
	objs, err := c.fs.List(c.levelDir(level))
	if err != nil {
		return nil, err
	}
	infos := make([]*ltx.FileInfo, 0, len(objs))
	for _, o := range objs {
		minTXID, maxTXID, err := ltx.ParseFilename(path.Base(o.Key))
		if err != nil || minTXID < seek {
			continue
		}
		created := o.ModTime.UTC()
		if useMetadata {
			if attrs, err := c.fs.Attributes(o.Key); err == nil {
				if t, err := time.Parse(time.RFC3339Nano, attrs.Metadata[metaTimestamp]); err == nil {
					created = t.UTC()
				}
			}
		}
		infos = append(infos, &ltx.FileInfo{Level: level, MinTXID: minTXID, MaxTXID: maxTXID, Size: o.Size, CreatedAt: created})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].MinTXID < infos[j].MinTXID })
	return ltx.NewFileInfoSliceIterator(infos), nil
}

func (c *bucketClient) OpenLTXFile(ctx context.Context, level int, minTXID, maxTXID ltx.TXID, offset, size int64) (io.ReadCloser, error) {
	key := c.key(level, minTXID, maxTXID)
	r, err := c.fs.GetReader(key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			err = os.ErrNotExist
		}
		return nil, litestream.NewLTXError("open", key, level, uint64(minTXID), uint64(maxTXID), err)
	}
	if offset > 0 {
		if _, err := r.Seek(offset, io.SeekStart); err != nil {
			_ = r.Close()
			return nil, err
		}
	}
	if size > 0 {
		return struct {
			io.Reader
			io.Closer
		}{io.LimitReader(r, size), r}, nil
	}
	return r, nil
}

func (c *bucketClient) WriteLTXFile(ctx context.Context, level int, minTXID, maxTXID ltx.TXID, rd io.Reader) (*ltx.FileInfo, error) {
	var head bytes.Buffer
	hdr, _, err := ltx.PeekHeader(io.TeeReader(rd, &head))
	if err != nil {
		return nil, fmt.Errorf("extract timestamp from LTX header: %w", err)
	}
	ts := time.UnixMilli(hdr.Timestamp).UTC()
	key := c.key(level, minTXID, maxTXID)
	w, err := c.fs.NewWriter(key, &blob.WriterOptions{
		ContentType: "application/octet-stream",
		Metadata:    map[string]string{metaTimestamp: ts.Format(time.RFC3339Nano)},
	})
	if err != nil {
		return nil, err
	}
	n, err := io.Copy(w, io.MultiReader(&head, rd))
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return &ltx.FileInfo{Level: level, MinTXID: minTXID, MaxTXID: maxTXID, Size: n, CreatedAt: ts}, nil
}

func (c *bucketClient) DeleteLTXFiles(ctx context.Context, a []*ltx.FileInfo) error {
	for _, info := range a {
		if err := c.fs.Delete(c.key(info.Level, info.MinTXID, info.MaxTXID)); err != nil && !errors.Is(err, blob.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (c *bucketClient) DeleteAll(ctx context.Context) error {
	return errors.Join(c.fs.DeletePrefix(c.root + "/")...)
}
