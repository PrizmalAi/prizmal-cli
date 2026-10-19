package launch

// subagentModel holds the dedicated subagent model for this process. It is
// package state because a launch resolves its model before the runner runs,
// and a subagent model selects an environment variable the runner sets rather
// than a second launch argument.
//
// It lives beside the readers, the Claude Code and Codex adapters: both reach
// the harness through an environment variable or an argument of their own, so
// until a third adapter claims the flag this state has two consumers, and it
// sits in the package they import rather than in either one of them.
var subagentModel string

// SetSubagentModel records the dedicated subagent model for this launch.
// main.go wires it from --subagent-model.
func SetSubagentModel(model string) { subagentModel = model }

// SelectedSubagentModel returns the dedicated subagent model, or "" when
// subagents inherit the session model.
func SelectedSubagentModel() string { return subagentModel }
