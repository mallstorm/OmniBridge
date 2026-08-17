package mcpcontrol

import "testing"

func TestStdioRequiresExplicitProcessPermission(t *testing.T) {
	c := New(nil)
	_, err := c.Upsert(Server{ID: "x", Name: "X", Transport: "stdio", Command: "tool", Enabled: true, AutoStart: true})
	if err == nil {
		t.Fatal("expected guard")
	}
	s, err := c.Upsert(Server{ID: "x", Name: "X", Transport: "stdio", Command: "tool", Enabled: true, AutoStart: true, AllowProcess: true})
	if err != nil || s.ID != "x" {
		t.Fatalf("%v %+v", err, s)
	}
}
func TestHTTPValidation(t *testing.T) {
	c := New(nil)
	if _, err := c.Upsert(Server{Name: "A", Transport: "http", URL: "file:///tmp/x"}); err == nil {
		t.Fatal("file URL must be rejected")
	}
}
