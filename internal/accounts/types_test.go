package accounts

import "testing"

func TestAccountCatalogContainsOnlySupportedAccountConnectors(t *testing.T) {
	c := Catalog()
	if len(c) != 2 {
		t.Fatalf("account catalog must contain exactly ChatGPT and Gemini, got %d: %+v", len(c), c)
	}
	seen := map[string]bool{}
	for _, x := range c {
		if x.Status != "supported" {
			t.Fatalf("account catalog must not expose unavailable placeholders: %+v", x)
		}
		seen[x.ID] = true
	}
	if !seen["chatgpt-codex"] || !seen["gemini-developer-oauth"] {
		t.Fatalf("expected ChatGPT and Gemini account connectors only: %+v", seen)
	}
	for _, forbidden := range []string{"qwen-account", "deepseek-account", "claude-subscription", "gemini-cli-subscription"} {
		if seen[forbidden] {
			t.Fatalf("unsupported account connector leaked into catalog: %s", forbidden)
		}
	}
}

func TestLoginStatusPreservesAccountIdentity(t *testing.T) {
	m := NewManager(t.TempDir(), nil)
	m.pending["a"] = &pendingLogin{accountID: "acct", status: LoginStatus{AccountID: "acct", AttemptID: "a", Status: "pending"}}
	got, ok := m.LoginStatus("a")
	if !ok || got.AccountID != "acct" || got.Status != "pending" {
		t.Fatalf("bad status: %+v ok=%v", got, ok)
	}
}
