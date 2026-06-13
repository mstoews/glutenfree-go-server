package appstore

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
)

// parseCertificates parses one or more certificates from PEM or DER bytes.
func parseCertificates(raw []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate

	// PEM (possibly multiple CERTIFICATE blocks).
	rest := raw
	for {
		block, r := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = r
		if block.Type == "CERTIFICATE" {
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, err
			}
			certs = append(certs, c)
		}
	}
	if len(certs) > 0 {
		return certs, nil
	}

	// DER fallback (possibly concatenated).
	certs, err := x509.ParseCertificates(raw)
	if err != nil {
		return nil, err
	}
	if len(certs) == 0 {
		return nil, errors.New("no certificates found")
	}
	return certs, nil
}
