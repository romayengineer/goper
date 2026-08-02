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

	cert, certDER, err := caCertificate(key)
	if err != nil {
		return nil, err
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

// caCertificate creates a self-signed CA certificate and returns both the
// parsed certificate and its DER encoding.
func caCertificate(key *ecdsa.PrivateKey) (*x509.Certificate, []byte, error) {
	template, err := caCertificateTemplate(key)
	if err != nil {
		return nil, nil, err
	}
	return createCertificate(key, template)
}

// caCertificateTemplate builds the self-signed CA template with a random serial.
func caCertificateTemplate(key *ecdsa.PrivateKey) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate serial: %w", err)
	}

	return &x509.Certificate{
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
	}, nil
}

// createCertificate creates and parses a self-signed certificate.
func createCertificate(key *ecdsa.PrivateKey, template *x509.Certificate) (*x509.Certificate, []byte, error) {
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create CA cert: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA cert: %w", err)
	}

	return cert, certDER, nil
}

func (ca *CA) TLSCertificate() tls.Certificate {
	return ca.TLS
}

func (ca *CA) Certificate() *x509.Certificate {
	return ca.Cert
}

func (ca *CA) PrivateKey() crypto.PrivateKey {
	return ca.Key
}

func (ca *CA) CertPool() *x509.CertPool {
	return ca.certPool
}
