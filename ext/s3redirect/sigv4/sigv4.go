// Package sigv4 presigns S3 GET URLs (AWS Signature Version 4, query-string auth): a pure function of the bucket
// config, so it needs nothing from PocketBase's S3 client.
package sigv4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Config is an S3-compatible bucket.
type Config struct {
	Endpoint  string // host or URL; http:// is kept, https is the default
	Bucket    string
	Region    string // default us-east-1
	AccessKey string
	SecretKey string
	PathStyle bool // bucket in the path (e.g. MinIO) instead of the host name
}

// PresignGet returns a SigV4 query-signed GET URL for key, valid for ttl from now (clamped to 1s..7 days, the
// SigV4 maximum). Extra response overrides (S3's response-content-disposition, response-content-type, ...) are
// signed into the URL.
func (s3 Config) PresignGet(key string, ttl time.Duration, now time.Time, overrides url.Values) (string, error) {
	if s3.Endpoint == "" || s3.Bucket == "" || s3.AccessKey == "" || s3.SecretKey == "" {
		return "", errors.New("sigv4: incomplete config for signing")
	}
	if ttl < time.Second {
		ttl = time.Second
	}
	if ttl > 7*24*time.Hour {
		ttl = 7 * 24 * time.Hour
	}
	region := s3.Region
	if region == "" {
		region = "us-east-1"
	}

	scheme, host := "https", strings.TrimRight(s3.Endpoint, "/")
	if after, ok := strings.CutPrefix(host, "https://"); ok {
		host = after
	} else if after, ok := strings.CutPrefix(host, "http://"); ok {
		host, scheme = after, "http"
	}
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}

	objectPath := "/" + uriEncode(strings.TrimLeft(key, "/"), false)
	if s3.PathStyle {
		objectPath = "/" + uriEncode(s3.Bucket, false) + objectPath
	} else {
		host = s3.Bucket + "." + host
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
	q.Set("X-Amz-Credential", s3.AccessKey+"/"+scope)
	q.Set("X-Amz-Date", dateTime)
	q.Set("X-Amz-Expires", fmt.Sprint(int64(ttl/time.Second)))
	q.Set("X-Amz-SignedHeaders", "host")

	canonicalQuery := presignQuery(q)
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
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := hmacSHA256(hmacSHA256(hmacSHA256(hmacSHA256([]byte("AWS4"+s3.SecretKey), date), region), "s3"), "aws4_request")
	signature := fmt.Sprintf("%x", hmacSHA256(signingKey, stringToSign))

	return scheme + "://" + host + objectPath + "?" + canonicalQuery + "&X-Amz-Signature=" + signature, nil
}

func presignQuery(q url.Values) string {
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

func sha256Hex(content []byte) string {
	h := sha256.Sum256(content)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key []byte, content string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(content))
	return mac.Sum(nil)
}

// SignedURL is [Config.PresignGet] for key with the given arguments, so a Config is an ext/s3redirect Signer.
func (s3 Config) SignedURL(key string, ttl time.Duration, now time.Time, overrides url.Values) (string, error) {
	return s3.PresignGet(key, ttl, now, overrides)
}
