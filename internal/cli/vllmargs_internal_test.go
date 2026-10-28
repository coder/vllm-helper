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

// EXTRA_ARGS is free text, so a key can arrive inline, as the last token, or
// under another credential-shaped flag; none of them may survive a dry run.
func TestRedactServeArgsCoversEverySpelling(t *testing.T) {
	for _, args := range [][]string{
		{"vllm", "serve", "m", "--api-key=sk-secret", "--port", "8001"},
		{"vllm", "serve", "m", "--port", "8001", "--api-key=sk-secret"},
		{"vllm", "serve", "m", "--hf-token", "sk-secret"},
	} {
		got := shellJoin(redactServeArgs(args))
		if containsSecret(got, "sk-secret") {
			t.Errorf("secret was not redacted: %s", got)
		}
		if !containsSecret(got, "<redacted>") {
			t.Errorf("redacted placeholder missing: %s", got)
		}
	}
	// A real flag that merely contains "key" is left alone, and so is the
	// value that follows it.
	got := shellJoin(redactServeArgs([]string{"vllm", "--ssl-keyfile", "/etc/k.pem", "--port", "8001"}))
	if !containsSecret(got, "/etc/k.pem") {
		t.Errorf("non-secret flag was redacted: %s", got)
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
