package proxy

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type CA struct {
	Cert     *x509.Certificate
	Key      crypto.PrivateKey
	TLS      tls.Certificate
	certPool *x509.CertPool
}

func GenerateCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "goper MITM CA",
			Organization: []string{"goper"},
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create CA cert: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("parse CA cert: %w", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(cert)

	return &CA{
		Cert:     cert,
		Key:      key,
		TLS:      tls.Certificate{Certificate: [][]byte{certDER}, PrivateKey: key},
		certPool: pool,
	}, nil
}

func LoadOrCreateCA(dir string) (*CA, error) {
	certPath := filepath.Join(dir, "ca-cert.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")

	if _, err := os.Stat(certPath); err == nil {
		certPEM, err := os.ReadFile(certPath)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read CA key: %w", err)
		}

		tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("parse CA key pair: %w", err)
		}

		cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("parse CA cert: %w", err)
		}

		pool := x509.NewCertPool()
		pool.AddCert(cert)

		return &CA{
			Cert:     cert,
			TLS:      tlsCert,
			certPool: pool,
		}, nil
	}

	ca, err := GenerateCA()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create CA dir: %w", err)
	}

	certPEM, err := pemEncode(ca.Cert.Raw, "CERTIFICATE")
	if err != nil {
		return nil, err
	}
	keyBytes, err := x509.MarshalECPrivateKey(ca.Key.(*ecdsa.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("marshal CA key: %w", err)
	}
	keyPEM, err := pemEncode(keyBytes, "EC PRIVATE KEY")
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return nil, fmt.Errorf("write CA cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("write CA key: %w", err)
	}

	return ca, nil
}

type CertCache struct {
	mu   sync.RWMutex
	certs map[string]*tls.Certificate
	ca   *CA
}

func NewCertCache(ca *CA) *CertCache {
	return &CertCache{
		certs: make(map[string]*tls.Certificate),
		ca:    ca,
	}
}

func (cc *CertCache) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	host := hello.ServerName
	if host == "" {
		host = hello.Conn.LocalAddr().String()
	}

	cc.mu.RLock()
	cert, ok := cc.certs[host]
	cc.mu.RUnlock()
	if ok {
		return cert, nil
	}

	cc.mu.Lock()
	defer cc.mu.Unlock()

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

	certDER, err := x509.CreateCertificate(rand.Reader, template, cc.ca.Cert, &key.PublicKey, cc.ca.Key)
	if err != nil {
		return nil, fmt.Errorf("create host cert: %w", err)
	}

	return &tls.Certificate{
		Certificate: [][]byte{certDER, cc.ca.Cert.Raw},
		PrivateKey:  key,
	}, nil
}

func (ca *CA) CertPool() *x509.CertPool {
	return ca.certPool
}

func pemEncode(derBytes []byte, blockType string) ([]byte, error) {
	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: blockType, Bytes: derBytes}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
