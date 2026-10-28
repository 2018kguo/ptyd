package terminal

import "fmt"

// TerminalEnvironment carries display capabilities from the viewing terminal.
// Nil values explicitly remove a daemon-inherited setting such as NO_COLOR.
// It never carries provider credentials or changes native conversation identity.
type TerminalEnvironment map[string]*string

func TerminalEnvironmentNames() []string {
	return []string{"TERM", "COLORTERM", "TERM_PROGRAM", "NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "FORCE_COLOR"}
}

func (env TerminalEnvironment) Apply(cfg *CreateConfig) error {
	allowed := map[string]bool{}
	for _, key := range TerminalEnvironmentNames() {
		allowed[key] = true
	}
	for key, value := range env {
		if !allowed[key] {
			return fmt.Errorf("%q is not a terminal display setting", key)
		}
		if value == nil {
			delete(cfg.Env, key)
			continue
		}
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env[key] = *value
	}
	return nil
}
