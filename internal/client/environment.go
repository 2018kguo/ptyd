package client

import (
	"os"

	"github.com/2018kguo/ptyd/internal/terminal"
)

// TerminalEnvironment captures absence too: a terminal that has no NO_COLOR
// must not inherit it from the background daemon.
func TerminalEnvironment() terminal.TerminalEnvironment {
	env := terminal.TerminalEnvironment{}
	for _, key := range terminal.TerminalEnvironmentNames() {
		var value *string
		if text, ok := os.LookupEnv(key); ok {
			value = &text
		}
		env[key] = value
	}
	return env
}
