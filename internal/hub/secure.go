package hub

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"time"

	"laserline/internal/invite"
)

func newIdentity() (tls.Certificate, [32]byte, error) {
	var fingerprint [32]byte
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, fingerprint, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return tls.Certificate{}, fingerprint, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Laserline DX temporary session"},
		NotBefore:    time.Now().Add(-time.Minute), NotAfter: time.Now().Add(7 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, private)
	if err != nil {
		return tls.Certificate{}, fingerprint, err
	}
	fingerprint = sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private}, fingerprint, nil
}

// Dial pins the exact certificate in the invitation. This does not trust any
// arbitrary self-signed certificate and never falls back to plaintext.
func Dial(ctx context.Context, c invite.Code) (*tls.Conn, error) {
	config := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // Required for custom pin validation below.
		VerifyConnection: func(state tls.ConnectionState) error {
			if state.Version != tls.VersionTLS13 {
				return errors.New("TLS 1.3 required")
			}
			if len(state.PeerCertificates) != 1 {
				return errors.New("unexpected certificate chain")
			}
			hash := sha256.Sum256(state.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(hash[:], c.Fingerprint[:]) != 1 {
				return errors.New("certificate fingerprint mismatch; invitation may be wrong")
			}
			return nil
		},
	}
	dialer := tls.Dialer{Config: config, NetDialer: nil}
	nc, err := dialer.DialContext(ctx, "tcp", c.Address)
	if err != nil {
		return nil, fmt.Errorf("secure connection to %s: %w", c.Address, err)
	}
	return nc.(*tls.Conn), nil
}
