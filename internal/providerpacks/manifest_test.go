package providerpacks

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestSignedDataOnlyManifest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	m := Manifest{SchemaVersion: 1, ID: "example", Name: "Example", Version: "1", Endpoints: map[string]string{"chat-completions": "https://api.example/v1"}, AuthType: "bearer", Protocols: []string{"chat-completions"}, Signer: "operator"}
	b, _ := canonical(m)
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, b))
	r := Validate(m, map[string]string{"operator": base64.StdEncoding.EncodeToString(pub)})
	if !r.Valid || !r.Trusted || r.Executable {
		t.Fatalf("result=%+v", r)
	}
	m.Endpoints["chat-completions"] = "file:///secret"
	if Validate(m, map[string]string{"operator": base64.StdEncoding.EncodeToString(pub)}).Valid {
		t.Fatal("unsafe endpoint accepted")
	}
}
