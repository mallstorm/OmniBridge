package providerpacks

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
)

const Version = "1.0"

type DoctorTemplate struct {
	APIType        string `json:"api_type"`
	ModelPath      string `json:"model_path,omitempty"`
	ExpectedStatus int    `json:"expected_status,omitempty"`
}
type Manifest struct {
	SchemaVersion  int               `json:"schema_version"`
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Version        string            `json:"version"`
	Endpoints      map[string]string `json:"endpoints"`
	AuthType       string            `json:"auth_type"`
	Protocols      []string          `json:"protocols"`
	Headers        map[string]string `json:"headers,omitempty"`
	ModelsURL      string            `json:"models_url,omitempty"`
	SanitizeFields []string          `json:"sanitize_fields,omitempty"`
	Doctor         []DoctorTemplate  `json:"doctor,omitempty"`
	Signer         string            `json:"signer"`
	Signature      string            `json:"signature"`
}
type Result struct {
	Valid      bool     `json:"valid"`
	Trusted    bool     `json:"trusted"`
	Executable bool     `json:"executable"`
	Hash       string   `json:"hash"`
	Errors     []string `json:"errors,omitempty"`
	Manifest   Manifest `json:"manifest"`
}

func canonical(m Manifest) ([]byte, error) {
	m.Signature = ""
	sort.Strings(m.Protocols)
	sort.Strings(m.SanitizeFields)
	return json.Marshal(m)
}
func safeURL(raw string) bool {
	u, e := url.Parse(strings.TrimSpace(raw))
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
func Validate(m Manifest, trust map[string]string) Result {
	r := Result{Manifest: m, Executable: false}
	errs := []string{}
	if m.SchemaVersion != 1 {
		errs = append(errs, "schema_version_must_be_1")
	}
	if strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Version) == "" {
		errs = append(errs, "identity_fields_required")
	}
	if len(m.Endpoints) == 0 {
		errs = append(errs, "endpoints_required")
	}
	for k, v := range m.Endpoints {
		if strings.TrimSpace(k) == "" || !safeURL(v) {
			errs = append(errs, "invalid_endpoint")
		}
	}
	if m.ModelsURL != "" && !safeURL(m.ModelsURL) {
		errs = append(errs, "invalid_models_url")
	}
	allowed := map[string]bool{"bearer": true, "x-api-key": true, "api-key": true, "basic": true, "none": true}
	if !allowed[strings.ToLower(m.AuthType)] {
		errs = append(errs, "unsupported_auth_type")
	}
	b, e := canonical(m)
	if e != nil {
		errs = append(errs, "canonicalization_failed")
	} else {
		h := sha256.Sum256(b)
		r.Hash = hex.EncodeToString(h[:])
		encoded, ok := trust[m.Signer]
		if !ok {
			errs = append(errs, "untrusted_signer")
		} else {
			pub, de := base64.StdEncoding.DecodeString(encoded)
			sig, se := base64.StdEncoding.DecodeString(m.Signature)
			if de != nil || se != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(ed25519.PublicKey(pub), b, sig) {
				errs = append(errs, "invalid_signature")
			} else {
				r.Trusted = true
			}
		}
	}
	r.Errors = errs
	r.Valid = len(errs) == 0
	r.Manifest.Signature = ""
	return r
}
func Decode(raw []byte) (Manifest, error) {
	var m Manifest
	if !json.Valid(raw) {
		return m, errors.New("manifest is not valid JSON")
	}
	return m, json.Unmarshal(raw, &m)
}
