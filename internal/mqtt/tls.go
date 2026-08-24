package mqtt

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// TLSProfile is the resolved, file-path-free-of-tilde form of a broker's TLS
// settings. internal/config produces it; this file turns it into a
// *tls.Config and nothing else.
type TLSProfile struct {
	Enabled            bool
	CAFile             string
	CertFile           string
	KeyFile            string
	ServerName         string
	InsecureSkipVerify bool
}

// BuildTLSConfig constructs a *tls.Config from a profile. A nil return with a
// nil error means "TLS disabled".
func BuildTLSConfig(p TLSProfile) (*tls.Config, error) {
	if !p.Enabled {
		return nil, nil
	}
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         p.ServerName,
		InsecureSkipVerify: p.InsecureSkipVerify, //nolint:gosec // opt-in, and banner-flagged for the whole session
	}
	if p.CAFile != "" {
		pem, err := os.ReadFile(p.CAFile)
		if err != nil {
			return nil, fmt.Errorf("reading ca_file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("ca_file %s contains no usable certificates", p.CAFile)
		}
		cfg.RootCAs = pool
	}
	switch {
	case p.CertFile != "" && p.KeyFile != "":
		cert, err := tls.LoadX509KeyPair(p.CertFile, p.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	case p.CertFile != "":
		return nil, errors.New("tls.cert_file set without tls.key_file")
	case p.KeyFile != "":
		return nil, errors.New("tls.key_file set without tls.cert_file")
	}
	return cfg, nil
}

// isTLSVerificationError reports whether err is a certificate problem, which
// is terminal — retrying will not make the certificate valid.
func isTLSVerificationError(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var recordHdr tls.RecordHeaderError
	var certVerify *tls.CertificateVerificationError
	return errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostname) ||
		errors.As(err, &invalid) ||
		errors.As(err, &recordHdr) ||
		errors.As(err, &certVerify)
}
