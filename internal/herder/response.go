package herder

type CLIResponse[T any] struct {
	ID     string `json:"id"`
	Result *T     `json:"result"`
}

type AgentListResult struct {
	Type   string      `json:"type"`
	Agents []AgentInfo `json:"agents"`
}

type AgentInfo struct {
	PaneID       string            `json:"pane_id"`
	Agent        *string           `json:"agent"`
	CWD          *string           `json:"cwd"`
	AgentSession *AgentSessionInfo `json:"agent_session"`
}

type AgentSessionInfo struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

type SessionSnapshotResult struct {
	Type     string          `json:"type"`
	Snapshot SessionSnapshot `json:"snapshot"`
}

type SessionSnapshot struct {
	Workspaces []WorkspaceInfo `json:"workspaces"`
	Panes      []PaneInfo      `json:"panes"`
}

type WorkspaceInfo struct {
	WorkspaceID string `json:"workspace_id"`
	Label       string `json:"label"`
}

type PaneInfo struct {
	Agent        string            `json:"agent"`
	AgentStatus  string            `json:"agent_status"`
	CWD          string            `json:"cwd"`
	Title        string            `json:"terminal_title_stripped"`
	Tokens       map[string]string `json:"tokens"`
	AgentSession *AgentSessionInfo `json:"agent_session"`
	PaneID       string            `json:"pane_id"`
	WorkspaceID  string            `json:"workspace_id"`
}

type ProcessInfoResult struct {
	Type        string          `json:"type"`
	ProcessInfo PaneProcessInfo `json:"process_info"`
}

type PaneProcessInfo struct {
	PaneID              string              `json:"pane_id"`
	ForegroundProcesses []ForegroundProcess `json:"foreground_processes"`
}

type ForegroundProcess struct {
	PID  uint32 `json:"pid"`
	Name string `json:"name"`
}
