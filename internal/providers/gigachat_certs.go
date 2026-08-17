package providers

import (
	"crypto/x509"
	_ "embed"
	"fmt"
	"os"
)

// Russian Trusted Root CA is the application-level trust anchor required by
// GigaChat API. It is embedded so a normal OmniBridge install does not require
// modifying the Windows certificate store or disabling TLS verification.
// Source documented by GigaChat: https://gu-st.ru/content/lending/russian_trusted_root_ca_pem.crt
//
//go:embed certs/russian_trusted_root_ca.pem
var embeddedRussianTrustedRootCA []byte

func gigaCertPool(extraFile string) (*x509.CertPool, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(embeddedRussianTrustedRootCA) {
		return nil, fmt.Errorf("embedded Russian Trusted Root CA is invalid")
	}
	if extraFile != "" {
		if pem, readErr := os.ReadFile(extraFile); readErr == nil && len(pem) > 0 {
			if !roots.AppendCertsFromPEM(pem) {
				return nil, fmt.Errorf("GigaChat extra CA bundle is invalid")
			}
		}
	}
	return roots, nil
}

func embeddedGigaCAValid() bool {
	pool := x509.NewCertPool()
	return pool.AppendCertsFromPEM(embeddedRussianTrustedRootCA)
}

// GigaCATrustStatus is secret-free diagnostic metadata for the local admin API.
func (m *Manager) GigaCATrustStatus() map[string]any {
	extra := false
	if m != nil && m.CAFile != "" {
		if st, err := os.Stat(m.CAFile); err == nil && st.Size() > 0 {
			extra = true
		}
	}
	return map[string]any{
		"embedded_root":    embeddedGigaCAValid(),
		"system_roots":     true,
		"extra_bundle":     extra,
		"tls_verify":       true,
		"root_common_name": "Russian Trusted Root CA",
	}
}
