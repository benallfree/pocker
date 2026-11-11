package s3_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"ganigeorgiev.com/pocketbase/pocketbase/tools/filesystem/internal/s3blob/s3"
)

// AWS's documented presigned URL example (IAM user guide, "Authenticating Requests: Using Query Parameters").
func TestPresignGetAWSExample(t *testing.T) {
	c := &s3.S3{Endpoint: "s3.amazonaws.com", Bucket: "examplebucket", Region: "us-east-1",
		AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	got, err := c.PresignGet("test.txt", 86400*time.Second, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://examplebucket.s3.amazonaws.com/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256" +
		"&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request" +
		"&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host" +
		"&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestPresignGetPathStyle(t *testing.T) {
	c := &s3.S3{Endpoint: "http://127.0.0.1:9000/", Bucket: "b", AccessKey: "k", SecretKey: "s", UsePathStyle: true}
	got, err := c.PresignGet("instances/abc/storage/col/rec/a b+c.png", time.Minute, time.Unix(0, 0),
		url.Values{"response-content-disposition": {`inline; filename="a b+c.png"`}})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "http" || u.Host != "127.0.0.1:9000" || u.EscapedPath() != "/b/instances/abc/storage/col/rec/a%20b%2Bc.png" {
		t.Fatalf("unexpected URL %s (path %s)", got, u.EscapedPath())
	}
	q := u.Query()
	if q.Get("X-Amz-Expires") != "60" || !strings.Contains(q.Get("X-Amz-Credential"), "/us-east-1/s3/aws4_request") {
		t.Fatalf("unexpected query %v", q)
	}
	if q.Get("response-content-disposition") != `inline; filename="a b+c.png"` {
		t.Fatalf("override not carried: %v", q)
	}
}
