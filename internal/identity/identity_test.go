package identity

import (
	"errors"
	"strings"
	"testing"

	"github.com/del-boy/grove/internal/model"
)

func TestResolvePriority(t *testing.T) {
	environment := map[string]string{
		"GROVE_AGENT":            "grove",
		"PI_SESSION_ID":          "pi-session",
		"CLAUDE_CODE_SESSION_ID": "claude-session",
		"CODEX_THREAD_ID":        "codex-session",
		"GEMINI_SESSION_ID":      "gemini-session",
	}
	lookup := mapLookup(environment)

	got, err := Resolve(" command ", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got != "command" {
		t.Errorf("explicit result = %q, want command", got)
	}

	got, err = Resolve("", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got != "grove" {
		t.Errorf("GROVE_AGENT result = %q, want grove", got)
	}

	delete(environment, "GROVE_AGENT")
	got, err = Resolve("", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got != "pi:pi-session" {
		t.Errorf("PI_SESSION_ID result = %q", got)
	}

	delete(environment, "PI_SESSION_ID")
	got, err = Resolve("", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got != "claude-code:claude-session" {
		t.Errorf("CLAUDE_CODE_SESSION_ID result = %q", got)
	}

	delete(environment, "CLAUDE_CODE_SESSION_ID")
	got, err = Resolve("", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got != "codex:codex-session" {
		t.Errorf("CODEX_THREAD_ID result = %q", got)
	}

	delete(environment, "CODEX_THREAD_ID")
	got, err = Resolve("", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got != "gemini:gemini-session" {
		t.Errorf("GEMINI_SESSION_ID result = %q", got)
	}

	delete(environment, "GEMINI_SESSION_ID")
	got, err = Resolve("", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got != "human" {
		t.Errorf("fallback result = %q, want human", got)
	}
}

func TestResolveIgnoresEmptyEnvironmentValues(t *testing.T) {
	lookup := mapLookup(map[string]string{
		"GROVE_AGENT":     "",
		"PI_SESSION_ID":   "",
		"CODEX_THREAD_ID": "thread",
	})
	got, err := Resolve("", lookup)
	if err != nil {
		t.Fatal(err)
	}
	if got != "codex:thread" {
		t.Errorf("Resolve() = %q, want codex:thread", got)
	}
}

func TestResolveFirstKnownVariableWinsEvenWhenInvalid(t *testing.T) {
	lookup := mapLookup(map[string]string{
		"PI_SESSION_ID":   "bad\nvalue",
		"CODEX_THREAD_ID": "valid",
	})
	_, err := Resolve("", lookup)
	assertInvalidAgent(t, err)
}

func TestValidate(t *testing.T) {
	valid := map[string]string{
		"agent":                  "agent",
		" outer spaces ":         "outer spaces",
		"π-session":              "π-session",
		strings.Repeat("x", 200): strings.Repeat("x", 200),
		strings.Repeat("界", 200): strings.Repeat("界", 200),
	}
	for input, want := range valid {
		t.Run("valid", func(t *testing.T) {
			got, err := Validate(input)
			if err != nil {
				t.Fatalf("Validate(%q) returned %v", input, err)
			}
			if got != want {
				t.Errorf("Validate(%q) = %q, want %q", input, got, want)
			}
		})
	}

	invalid := []string{
		"",
		"   ",
		"line\nbreak",
		"agent\n",
		"tab\tvalue",
		"null\x00value",
		strings.Repeat("x", 201),
		strings.Repeat("界", 201),
		string([]byte{0xff}),
	}
	for _, input := range invalid {
		t.Run("invalid", func(t *testing.T) {
			_, err := Validate(input)
			assertInvalidAgent(t, err)
		})
	}
}

func TestResolveRejectsInvalidExplicitAndGroveAgent(t *testing.T) {
	_, err := Resolve("   ", mapLookup(nil))
	assertInvalidAgent(t, err)

	_, err = Resolve("", mapLookup(map[string]string{"GROVE_AGENT": "\n"}))
	assertInvalidAgent(t, err)
}

func mapLookup(values map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	}
}

func assertInvalidAgent(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var domainErr *model.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("error = %#v, want *model.Error", err)
	}
	if domainErr.Code != model.ErrorInvalidAgent || domainErr.ExitCode != model.ExitInvalidArguments {
		t.Errorf("error = %#v", domainErr)
	}
}
