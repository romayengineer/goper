package proxy

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"sync"
	"time"
)

type CAProvider interface {
	TLSCertificate() tls.Certificate
	Certificate() *x509.Certificate
	PrivateKey() crypto.PrivateKey
}

type CertStore interface {
	GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
	GetCertForHost(host string) (*tls.Certificate, error)
}

type CertCache struct {
	mu    sync.RWMutex
	certs map[string]*tls.Certificate
	ca    CAProvider
}

func NewCertCache(ca CAProvider) *CertCache {
	return &CertCache{
		certs: make(map[string]*tls.Certificate),
		ca:    ca,
	}
}

func (cc *CertCache) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := hostForHello(hello)

	cc.mu.RLock()
	cert, ok := cc.certs[host]
	cc.mu.RUnlock()
	if ok {
		return cert, nil
	}

	cc.mu.Lock()
	defer cc.mu.Unlock()

	return cc.certOrGenerate(host)
}

// hostForHello resolves the certificate host from the ClientHello, falling
// back to the connection's local address and finally "localhost".
func hostForHello(hello *tls.ClientHelloInfo) string {
	host := hello.ServerName
	if host == "" {
		host = localAddr(hello)
	}
	if host == "" {
		host = "localhost"
	}
	return host
}

// localAddr falls back to the connection's local address.
func localAddr(hello *tls.ClientHelloInfo) string {
	if hello.Conn != nil {
		return hello.Conn.LocalAddr().String()
	}
	return ""
}

// certOrGenerate returns the cached certificate for host, generating it on
// first use. The caller must hold the write lock.
func (cc *CertCache) certOrGenerate(host string) (*tls.Certificate, error) {
	if cert, ok := cc.certs[host]; ok {
		return cert, nil
	}

	cert, err := cc.generateCert(host)
	if err != nil {
		return nil, err
	}
	cc.certs[host] = cert
	return cert, nil
}

func (cc *CertCache) GetCertForHost(host string) (*tls.Certificate, error) {
	return cc.GetCertificate(&tls.ClientHelloInfo{ServerName: host})
}

func (cc *CertCache) generateCert(host string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate host key: %w", err)
	}

	return cc.signHostCert(host, key)
}

// signHostCert builds and signs a host certificate with the CA.
func (cc *CertCache) signHostCert(host string, key *ecdsa.PrivateKey) (*tls.Certificate, error) {
	template, err := hostCertificateTemplate(host, key)
	if err != nil {
		return nil, err
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, cc.ca.Certificate(), &key.PublicKey, cc.ca.PrivateKey())
	if err != nil {
		return nil, fmt.Errorf("create host cert: %w", err)
	}

	return &tls.Certificate{
		Certificate: [][]byte{certDER, cc.ca.Certificate().Raw},
		PrivateKey:  key,
	}, nil
}

// hostCertificateTemplate builds the certificate template for a host, using an
// IP SAN when the host parses as an IP address and DNS names otherwise.
func hostCertificateTemplate(host string, key *ecdsa.PrivateKey) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   host,
			Organization: []string{"goper"},
		},
		NotBefore:   time.Now().Add(-24 * time.Hour),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	return template, nil
}
