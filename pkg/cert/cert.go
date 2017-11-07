package cert

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"time"
)

type CertPemData struct {
	Crt []byte // PEM encoded
	Key []byte // PEM encoded
	//CertPemData *x509.CertPemData `json:"-"`
}

func NewCertificateFromDER(der [][]byte, privateKey *rsa.PrivateKey) (certificate *CertPemData, err error) {
	if len(der) < 1 {
		err = errors.New("can't create certificate from empty DER array")
		return
	}

	certificate = &CertPemData{}

	certBuffer := bytes.NewBuffer([]byte{})
	for _, cert := range der {
		_, err = x509.ParseCertificate(cert)
		if err != nil {
			return
		}

		pem.Encode(certBuffer, &pem.Block{Type: "CERTIFICATE", Bytes: cert})
	}
	certificate.Crt = certBuffer.Bytes()

	keyPem := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})
	certificate.Key = keyPem

	return
}

func (c *CertPemData) Certificate() (*x509.Certificate, error) {
	block, _ := pem.Decode(c.Crt)
	if block == nil {
		return nil, errors.New("no data found in Crt")
	}

	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	return certificate, nil
}

//func (lhs *CertPemData) Equal(rhs *CertPemData) bool {
//	return reflect.DeepEqual(lhs.Key, rhs.Key) && reflect.DeepEqual(lhs.Crt, rhs.Crt)
//}

//func (c *CertPemData) Domains() (domains []string) {
//	domains = append(domains, c.Certificate.DNSNames...)
//	// Add common name only if it was not in  DNSNames to avoid duplicate domains
//	found := false
//	for _, domain := range domains {
//		if domain == c.Certificate.Subject.CommonName {
//			found = true
//		}
//	}
//
//	if !found {
//		domains = append(domains, c.Certificate.Subject.CommonName)
//	}
//
//	return
//}

func IsValid(c *x509.Certificate, t time.Time) bool {
	return !(t.Before(c.NotBefore) || t.After(c.NotAfter))
}

//func FresherCertificate(c1, c2 *CertPemData, t time.Time) *CertPemData {
//	c1Valid := c1.IsValid(t)
//	c2Valid := c1.IsValid(t)
//	if c1Valid {
//		if c2Valid {
//			if c2.Certificate.NotAfter.After(c1.Certificate.NotAfter) {
//				return c2
//			} else {
//				return c1
//			}
//		} else {
//			return c1
//		}
//	} else {
//		if c2Valid {
//			return c2
//		} else {
//			return c1
//		}
//	}
//}
