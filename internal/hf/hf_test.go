package hf

import (
	"strings"
	"testing"
)

func TestPatchSystemMessageCheck(t *testing.T) {
	const withCheck = `{%- for message in messages %}
    {%- set content = render_content(message.content, true)|trim %}
    {%- if message.role == "system" %}
        {%- if not loop.first %}
            {{- raise_exception('System message must be at the beginning.') }}
        {%- endif %}
    {%- elif message.role == "user" %}
        {{- '<|im_start|>' + message.role + '\n' + content + '<|im_end|>' + '\n' }}
    {%- endif %}
{%- endfor %}`

	t.Run("patches the exact known shape", func(t *testing.T) {
		patched, ok, err := patchSystemMessageCheck(withCheck)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Fatal("expected ok=true, got false")
		}
		if strings.Contains(patched, "raise_exception") {
			t.Error("patched template still contains the raise_exception call")
		}
		if !strings.Contains(patched, `if not loop.first`) {
			t.Error("patch should leave the surrounding if/endif guard intact")
		}
		if !strings.Contains(patched, `message.role == "system"`) {
			t.Error("patch should leave unrelated template logic untouched")
		}
	})

	t.Run("no-op when the check isn't present", func(t *testing.T) {
		const clean = `{%- for message in messages %}
    {{- message.content }}
{%- endfor %}`
		patched, ok, err := patchSystemMessageCheck(clean)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Error("expected ok=false for a template without the issue")
		}
		if patched != "" {
			t.Error("expected empty patched output when ok=false")
		}
	})

	t.Run("errors instead of silently no-op when the string is present but the shape doesn't match", func(t *testing.T) {
		const weirdShape = "System message must be at the beginning. (mentioned in a comment, not the real check)"
		_, ok, err := patchSystemMessageCheck(weirdShape)
		if ok {
			t.Error("expected ok=false when the regex can't safely match")
		}
		if err == nil {
			t.Error("expected an error flagging the unmatched shape, got nil")
		}
	})
}

func TestClampContext(t *testing.T) {
	cases := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{"caps a card that oversells the context", "131072", 40960, "40960"},
		{"leaves a value the model supports alone", "32768", 40960, "32768"},
		{"fills in the limit when the card says nothing", "", 40960, "40960"},
		{"keeps the value when the limit is unknown", "131072", 0, "131072"},
		{"allows exactly the limit", "40960", 40960, "40960"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampContext(tc.value, tc.limit); got != tc.want {
				t.Errorf("clampContext(%q, %d) = %q, want %q", tc.value, tc.limit, got, tc.want)
			}
		})
	}
}
