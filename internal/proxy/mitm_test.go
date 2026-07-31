package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestGenerateCA(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	if !ca.Cert.IsCA {
		t.Fatal("expected generated cert to be a CA")
	}
	if ca.Cert.Subject.CommonName != "goper MITM CA" {
		t.Fatalf("common name: got %q", ca.Cert.Subject.CommonName)
	}
	if ca.Key == nil {
		t.Fatal("expected CA to have a private key")
	}
	if len(ca.TLS.Certificate) == 0 {
		t.Fatal("expected CA to have TLS certificate data")
	}
	if ca.CertPool() == nil {
		t.Fatal("expected CA to have a cert pool")
	}
	if ca.TLSCertificate().Leaf == nil && len(ca.TLSCertificate().Certificate) == 0 {
		t.Fatal("TLSCertificate should return usable cert")
	}
}

func TestGenerateCACertificateAndPrivateKey(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	if ca.Certificate() != ca.Cert {
		t.Fatal("Certificate() should return the CA cert")
	}
	if ca.PrivateKey() != ca.Key {
		t.Fatal("PrivateKey() should return the CA key")
	}
}

func TestLoadOrCreateCA_Persists(t *testing.T) {
	dir := t.TempDir()

	ca1, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("first LoadOrCreateCA: %v", err)
	}

	certFile := filepath.Join(dir, "ca-cert.pem")
	keyFile := filepath.Join(dir, "ca-key.pem")
	if !fileExists(certFile) || !fileExists(keyFile) {
		t.Fatal("expected CA cert and key files to be written to disk")
	}

	ca2, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("second LoadOrCreateCA: %v", err)
	}

	if ca1.Cert.SerialNumber.Cmp(ca2.Cert.SerialNumber) != 0 {
		t.Fatal("expected CA to be reloaded from disk (same serial)")
	}
}

func TestLoadOrCreateCA_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	ca, err := LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateCA on empty dir: %v", err)
	}
	if !ca.Cert.IsCA {
		t.Fatal("expected newly generated CA")
	}
}

func TestCertCacheGenerateAndCache(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	cc := NewCertCache(ca)

	cert1, err := cc.GetCertForHost("api.example.com")
	if err != nil {
		t.Fatalf("GetCertForHost: %v", err)
	}
	if cert1 == nil {
		t.Fatal("expected a certificate")
	}

	cert2, err := cc.GetCertForHost("api.example.com")
	if err != nil {
		t.Fatalf("GetCertForHost (cached): %v", err)
	}
	if cert1 != cert2 {
		t.Fatal("expected same cached certificate pointer for same host")
	}
}

func TestCertCacheCertificateValidForHost(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	cc := NewCertCache(ca)

	cert, err := cc.GetCertForHost("api.example.com")
	if err != nil {
		t.Fatalf("GetCertForHost: %v", err)
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "api.example.com" {
		t.Fatalf("DNS names: got %v", leaf.DNSNames)
	}

	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	if _, err := leaf.Verify(x509.VerifyOptions{
		DNSName: "api.example.com",
		Roots:   roots,
	}); err != nil {
		t.Fatalf("leaf does not verify against CA: %v", err)
	}
}

func TestCertCacheIPHost(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	cc := NewCertCache(ca)

	cert, err := cc.GetCertForHost("192.168.1.10")
	if err != nil {
		t.Fatalf("GetCertForHost: %v", err)
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if len(leaf.IPAddresses) != 1 || leaf.IPAddresses[0].String() != "192.168.1.10" {
		t.Fatalf("IP addresses: got %v", leaf.IPAddresses)
	}
}

func TestCertCacheConcurrent(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	cc := NewCertCache(ca)

	var wg sync.WaitGroup
	hosts := []string{"a.com", "b.com", "c.com", "d.com"}

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			host := hosts[i%len(hosts)]
			if _, err := cc.GetCertForHost(host); err != nil {
				t.Errorf("GetCertForHost(%s): %v", host, err)
			}
		}(i)
	}

	wg.Wait()
}

func TestCertCacheGetCertificateFallback(t *testing.T) {
	ca, err := GenerateCA()
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	cc := NewCertCache(ca)

	// No SNI hostname -> falls back to local addr string; should not error.
	cert, err := cc.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatalf("GetCertificate with empty SNI: %v", err)
	}
	if cert == nil {
		t.Fatal("expected cert for empty SNI fallback")
	}
}

func TestCertCacheImplementsCertStore(t *testing.T) {
	ca, _ := GenerateCA()
	var _ CertStore = NewCertCache(ca)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
