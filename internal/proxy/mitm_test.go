package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateCA(t *testing.T) {
	ca, err := GenerateCA()
	require.NoError(t, err)

	assert.True(t, ca.Cert.IsCA, "expected generated cert to be a CA")
	assert.Equal(t, "goper MITM CA", ca.Cert.Subject.CommonName)
	assert.NotNil(t, ca.Key, "expected CA to have a private key")
	assert.NotEmpty(t, ca.TLS.Certificate, "expected CA to have TLS certificate data")
	assert.NotNil(t, ca.CertPool(), "expected CA to have a cert pool")
	assert.False(t, ca.TLSCertificate().Leaf == nil && len(ca.TLSCertificate().Certificate) == 0, "TLSCertificate should return usable cert")
}

func TestGenerateCACertificateAndPrivateKey(t *testing.T) {
	ca, err := GenerateCA()
	require.NoError(t, err)

	assert.Same(t, ca.Cert, ca.Certificate())
	assert.Same(t, ca.Key, ca.PrivateKey())
}

func TestLoadOrCreateCA_Persists(t *testing.T) {
	dir := t.TempDir()

	ca1, err := LoadOrCreateCA(dir)
	require.NoError(t, err)

	certFile := filepath.Join(dir, "ca-cert.pem")
	keyFile := filepath.Join(dir, "ca-key.pem")
	assert.True(t, fileExists(certFile), "expected CA cert file on disk")
	assert.True(t, fileExists(keyFile), "expected CA key file on disk")

	ca2, err := LoadOrCreateCA(dir)
	require.NoError(t, err)

	assert.Equal(t, 0, ca1.Cert.SerialNumber.Cmp(ca2.Cert.SerialNumber), "expected CA to be reloaded from disk")
	assert.NotNil(t, ca2.Key, "expected reloaded CA to carry its private key")
}

func TestCertCacheWorksWithReloadedCA(t *testing.T) {
	dir := t.TempDir()

	ca1, err := LoadOrCreateCA(dir)
	require.NoError(t, err)

	ca2, err := LoadOrCreateCA(dir)
	require.NoError(t, err)
	assert.NotNil(t, ca2.Key, "expected reloaded CA to carry its private key")

	cc := NewCertCache(ca2)
	cert, err := cc.GetCertForHost("api.example.com")
	require.NoError(t, err, "reloaded CA must be able to sign leaf certs")
	require.NotNil(t, cert)

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)

	roots := x509.NewCertPool()
	roots.AddCert(ca2.Cert)
	_, err = leaf.Verify(x509.VerifyOptions{
		DNSName: "api.example.com",
		Roots:   roots,
	})
	assert.NoError(t, err, "leaf signed by reloaded CA should verify")
	assert.Equal(t, ca1.Cert.SerialNumber, ca2.Cert.SerialNumber, "CA must be reloaded, not regenerated")
}

func TestLoadOrCreateCA_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	ca, err := LoadOrCreateCA(dir)
	require.NoError(t, err)
	assert.True(t, ca.Cert.IsCA, "expected newly generated CA")
}

func TestCertCacheGenerateAndCache(t *testing.T) {
	ca, err := GenerateCA()
	require.NoError(t, err)
	cc := NewCertCache(ca)

	cert1, err := cc.GetCertForHost("api.example.com")
	require.NoError(t, err)
	require.NotNil(t, cert1)

	cert2, err := cc.GetCertForHost("api.example.com")
	require.NoError(t, err)
	assert.Same(t, cert1, cert2, "expected same cached certificate pointer for same host")
}

func TestCertCacheCertificateValidForHost(t *testing.T) {
	ca, err := GenerateCA()
	require.NoError(t, err)
	cc := NewCertCache(ca)

	cert, err := cc.GetCertForHost("api.example.com")
	require.NoError(t, err)

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)
	assert.Equal(t, []string{"api.example.com"}, leaf.DNSNames)

	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	_, err = leaf.Verify(x509.VerifyOptions{
		DNSName: "api.example.com",
		Roots:   roots,
	})
	assert.NoError(t, err, "leaf should verify against CA")
}

func TestCertCacheIPHost(t *testing.T) {
	ca, err := GenerateCA()
	require.NoError(t, err)
	cc := NewCertCache(ca)

	cert, err := cc.GetCertForHost("192.168.1.10")
	require.NoError(t, err)

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	require.NoError(t, err)
	require.Len(t, leaf.IPAddresses, 1)
	assert.Equal(t, "192.168.1.10", leaf.IPAddresses[0].String())
}

func TestCertCacheConcurrent(t *testing.T) {
	ca, err := GenerateCA()
	require.NoError(t, err)
	cc := NewCertCache(ca)

	var wg sync.WaitGroup
	hosts := []string{"a.com", "b.com", "c.com", "d.com"}
	errs := make(chan error, 100)

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			host := hosts[i%len(hosts)]
			if _, err := cc.GetCertForHost(host); err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)
	assert.Empty(t, errs, "no GetCertForHost errors expected")
}

func TestCertCacheGetCertificateFallback(t *testing.T) {
	ca, err := GenerateCA()
	require.NoError(t, err)
	cc := NewCertCache(ca)

	cert, err := cc.GetCertificate(&tls.ClientHelloInfo{})
	require.NoError(t, err)
	assert.NotNil(t, cert, "expected cert for empty SNI fallback")
}

func TestCertCacheImplementsCertStore(t *testing.T) {
	ca, _ := GenerateCA()
	var _ CertStore = NewCertCache(ca)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
