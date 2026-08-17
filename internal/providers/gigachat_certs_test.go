package providers

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestEmbeddedGigaChatRootCA(t *testing.T) {
	if !embeddedGigaCAValid() {
		t.Fatal("embedded Russian Trusted Root CA did not parse")
	}
	block, _ := pem.Decode(embeddedRussianTrustedRootCA)
	if block == nil {
		t.Fatal("missing PEM certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if cert.Subject.CommonName != "Russian Trusted Root CA" {
		t.Fatalf("unexpected CA CN %q", cert.Subject.CommonName)
	}
	if !cert.IsCA {
		t.Fatal("embedded certificate is not a CA")
	}
}
