package cli

import "testing"

func TestRedactServeArgs(t *testing.T) {
	got := shellJoin(redactServeArgs([]string{"vllm", "serve", "m", "--api-key", "sk-secret", "--port", "8001"}))
	if containsSecret(got, "sk-secret") {
		t.Fatalf("secret was not redacted: %s", got)
	}
	if !containsSecret(got, "<redacted>") {
		t.Fatalf("redacted placeholder missing: %s", got)
	}
}

func containsSecret(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
