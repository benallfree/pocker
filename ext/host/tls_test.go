package host

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func selfSigned(t *testing.T, dir, name string) (string, string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	cert, keyPath := filepath.Join(dir, "tls.cert"), filepath.Join(dir, "tls.key")
	_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	_ = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	return cert, keyPath
}

// renewed certificate files are picked up without a restart
func TestCertReloaderPicksUpRenewal(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := selfSigned(t, dir, "first.test")
	r, err := newCertReloader(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := r.GetCertificate(nil)
	if leaf, _ := x509.ParseCertificate(c.Certificate[0]); leaf.Subject.CommonName != "first.test" {
		t.Fatalf("first: %s", leaf.Subject.CommonName)
	}
	time.Sleep(10 * time.Millisecond)
	selfSigned(t, dir, "second.test")
	r.lastCheck = time.Time{}
	c, _ = r.GetCertificate(nil)
	leaf, _ := x509.ParseCertificate(c.Certificate[0])
	if leaf.Subject.CommonName != "second.test" {
		t.Fatalf("reload: still %s", leaf.Subject.CommonName)
	}
}
