package identity

import (
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/del-boy/grove/internal/model"
)

const MaxAgentCharacters = 200

type LookupEnv func(string) (string, bool)

var sessionVariables = []struct {
	name   string
	prefix string
}{
	{name: "PI_SESSION_ID", prefix: "pi:"},
	{name: "CLAUDE_CODE_SESSION_ID", prefix: "claude-code:"},
	{name: "CODEX_THREAD_ID", prefix: "codex:"},
	{name: "GEMINI_SESSION_ID", prefix: "gemini:"},
}

func Resolve(explicit string, lookup LookupEnv) (string, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if explicit != "" {
		return Validate(explicit)
	}
	if value, exists := lookup("GROVE_AGENT"); exists && value != "" {
		return Validate(value)
	}
	for _, variable := range sessionVariables {
		if value, exists := lookup(variable.name); exists && value != "" {
			return Validate(variable.prefix + value)
		}
	}
	return "human", nil
}

func Current(explicit string) (string, error) {
	return Resolve(explicit, os.LookupEnv)
}

func Validate(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", invalidAgent("The agent ID must use valid UTF-8.")
	}
	for _, character := range value {
		if !unicode.IsPrint(character) {
			return "", invalidAgent("The agent ID must contain only printable characters.")
		}
	}
	value = strings.TrimSpace(value)
	characters := utf8.RuneCountInString(value)
	if characters == 0 || characters > MaxAgentCharacters {
		return "", invalidAgent("The agent ID must contain 1 through 200 characters.")
	}
	return value, nil
}

func invalidAgent(message string) *model.Error {
	return model.NewError(model.ErrorInvalidAgent, model.ExitInvalidArguments, message, nil)
}
