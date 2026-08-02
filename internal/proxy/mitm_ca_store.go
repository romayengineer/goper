package proxy

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

func LoadOrCreateCA(dir string) (*CA, error) {
	certPath := filepath.Join(dir, "ca-cert.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")

	if _, err := os.Stat(certPath); err == nil {
		return loadCA(certPath, keyPath)
	}

	return createAndPersistCA(dir, certPath, keyPath)
}

// createAndPersistCA generates a fresh CA and writes it to disk.
func createAndPersistCA(dir, certPath, keyPath string) (*CA, error) {
	ca, err := GenerateCA()
	if err != nil {
		return nil, err
	}
	if err := persistCA(ca, dir, certPath, keyPath); err != nil {
		return nil, err
	}
	return ca, nil
}

// persistCA writes a newly generated CA key pair to disk.
func persistCA(ca *CA, dir, certPath, keyPath string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create CA dir: %w", err)
	}

	certPEM, keyPEM, err := encodeCAFiles(ca)
	if err != nil {
		return err
	}

	return writeCAFiles(certPath, keyPath, certPEM, keyPEM)
}

// encodeCAFiles encodes the CA certificate and key as PEM.
func encodeCAFiles(ca *CA) (certPEM, keyPEM []byte, err error) {
	certPEM, err = pemEncode(ca.Cert.Raw, "CERTIFICATE")
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err = encodeCAKeyPEM(ca)
	if err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

// writeCAFiles persists the CA certificate and key with appropriate
// permissions: the cert is public so it can be served via the API and
// installed in browsers, while the key stays private to the process owner.
func writeCAFiles(certPath, keyPath string, certPEM, keyPEM []byte) error {
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil { // #nosec G306 -- the CA certificate is public: it must be readable so it can be served via the API and installed in browsers
		return fmt.Errorf("write CA cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("write CA key: %w", err)
	}
	return nil
}

// encodeCAKeyPEM marshals the CA private key to PEM.
func encodeCAKeyPEM(ca *CA) ([]byte, error) {
	keyBytes, err := x509.MarshalECPrivateKey(ca.Key.(*ecdsa.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("marshal CA key: %w", err)
	}
	return pemEncode(keyBytes, "EC PRIVATE KEY")
}

// loadCA reads and parses a previously persisted CA key pair from disk.
func loadCA(certPath, keyPath string) (*CA, error) {
	certPEM, keyPEM, err := readCAPairFiles(certPath, keyPath)
	if err != nil {
		return nil, err
	}

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse CA key pair: %w", err)
	}

	return parseCAPair(tlsCert)
}

// readCAPairFiles reads the CA certificate and key files.
func readCAPairFiles(certPath, keyPath string) (certPEM, keyPEM []byte, err error) {
	certPEM, err = os.ReadFile(certPath) // #nosec G304 -- path derives from the configured CA dir
	if err != nil {
		return nil, nil, fmt.Errorf("read CA cert: %w", err)
	}
	keyPEM, err = os.ReadFile(keyPath) // #nosec G304 -- path derives from the configured CA dir
	if err != nil {
		return nil, nil, fmt.Errorf("read CA key: %w", err)
	}
	return certPEM, keyPEM, nil
}

// parseCAPair builds a CA from an already-parsed TLS key pair.
func parseCAPair(tlsCert tls.Certificate) (*CA, error) {
	cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse CA cert: %w", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(cert)

	return &CA{
		Cert:     cert,
		Key:      tlsCert.PrivateKey,
		TLS:      tlsCert,
		certPool: pool,
	}, nil
}

func pemEncode(derBytes []byte, blockType string) ([]byte, error) {
	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: blockType, Bytes: derBytes}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
