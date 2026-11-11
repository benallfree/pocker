package s3redirect

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Target is an S3-compatible bucket that downloads can be signed for. Its fields mirror PocketBase's S3 settings.
type Target struct {
	Endpoint       string // with or without scheme; https is assumed when missing
	Bucket         string
	Region         string // "" means us-east-1; R2 uses "auto"
	AccessKey      string
	Secret         string
	ForcePathStyle bool
	Prefix         string // prepended to every key (e.g. "instances/<id>/storage/"); "" for an app's own S3
}

// Valid reports whether t has everything needed to sign.
func (t *Target) Valid() bool {
	return t != nil && t.Endpoint != "" && t.Bucket != "" && t.AccessKey != "" && t.Secret != ""
}

// PresignGet returns a SigV4 query-signed GET URL for key, valid for ttl from now. Extra response overrides (S3's
// response-content-disposition, response-content-type, ...) are signed into the URL.
func (t *Target) PresignGet(key string, ttl time.Duration, now time.Time, overrides url.Values) (string, error) {
	if !t.Valid() {
		return "", fmt.Errorf("s3redirect: incomplete bucket config")
	}
	if ttl < time.Second {
		ttl = time.Second
	}
	if ttl > 7*24*time.Hour { // SigV4 maximum
		ttl = 7 * 24 * time.Hour
	}
	region := t.Region
	if region == "" {
		region = "us-east-1"
	}

	scheme, host := "https", strings.TrimRight(t.Endpoint, "/")
	if after, ok := strings.CutPrefix(host, "https://"); ok {
		host = after
	} else if after, ok := strings.CutPrefix(host, "http://"); ok {
		host, scheme = after, "http"
	}
	if i := strings.IndexByte(host, '/'); i >= 0 { // a path on the endpoint isn't supported by PocketBase either
		host = host[:i]
	}

	objectPath := "/" + escapePath(strings.TrimLeft(t.Prefix+key, "/"))
	if t.ForcePathStyle {
		objectPath = "/" + escapePath(t.Bucket) + objectPath
	} else {
		host = t.Bucket + "." + host
	}

	now = now.UTC()
	date := now.Format("20060102")
	dateTime := now.Format("20060102T150405Z")
	scope := date + "/" + region + "/s3/aws4_request"

	q := url.Values{}
	for k, vs := range overrides {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	q.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	q.Set("X-Amz-Credential", t.AccessKey+"/"+scope)
	q.Set("X-Amz-Date", dateTime)
	q.Set("X-Amz-Expires", fmt.Sprint(int64(ttl/time.Second)))
	q.Set("X-Amz-SignedHeaders", "host")

	canonicalQuery := canonicalQueryString(q)
	canonicalRequest := strings.Join([]string{
		"GET",
		objectPath,
		canonicalQuery,
		"host:" + host + "\n",
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		dateTime,
		scope,
		sha256Hex(canonicalRequest),
	}, "\n")

	key1 := hmacSHA256([]byte("AWS4"+t.Secret), date)
	key2 := hmacSHA256(key1, region)
	key3 := hmacSHA256(key2, "s3")
	signingKey := hmacSHA256(key3, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	return scheme + "://" + host + objectPath + "?" + canonicalQuery + "&X-Amz-Signature=" + signature, nil
}

func canonicalQueryString(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// escapePath URI-encodes each segment of an object key (AWS rules: everything but unreserved characters; "/" kept).
func escapePath(p string) string { return uriEncode(p, false) }

// uriEncode follows the SigV4 UriEncode rules: A-Z a-z 0-9 - _ . ~ stay, everything else is %XX (uppercase).
// "/" is encoded only when encodeSlash is true (query strings).
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key []byte, s string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return m.Sum(nil)
}
