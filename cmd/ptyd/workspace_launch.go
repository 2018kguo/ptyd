package main

import "github.com/2018kguo/ptyd/internal/terminal"

type workspaceAgentAccess uint8

const (
	workspaceFullAccess workspaceAgentAccess = iota
	workspaceCLISettings
)

func (access workspaceAgentAccess) launch(command string) terminal.CreateConfig {
	cfg := terminal.CreateConfig{Command: command, Name: command, Restore: terminal.RestoreAuto}
	if access != workspaceFullAccess {
		return cfg
	}
	switch command {
	case "codex":
		cfg.Args = []string{"--dangerously-bypass-approvals-and-sandbox"}
	case "claude":
		cfg.Args = []string{"--dangerously-skip-permissions"}
	}
	return cfg
}

func workspaceCustomLaunch(command, shell string) terminal.CreateConfig {
	return terminal.CreateConfig{Command: shell, Args: []string{"-lc", command}, Name: clipStatus(command, 40), Restore: terminal.RestoreNone}
}
