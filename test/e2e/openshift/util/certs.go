package util

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
)

func CertificateFromPEM(crt []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(crt)
	if block == nil {
		return nil, errors.New("no data found in Crt")
	}

	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	return certificate, nil
}
