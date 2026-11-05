package agents

// AgentType represents the execution runner type of an automated review agent.
type AgentType string

const (
	AgentTypeAI        AgentType = "ai"
	AgentTypeInTree    AgentType = "in_tree"
	AgentTypeCASBinary AgentType = "cas_binary"
	AgentTypeExec      AgentType = "exec"
)

// AllAgentTypes lists all valid agent runner types.
var AllAgentTypes = []AgentType{
	AgentTypeAI,
	AgentTypeInTree,
	AgentTypeCASBinary,
	AgentTypeExec,
}

// IsValid reports whether the agent type is valid.
func (a AgentType) IsValid() bool {
	switch a {
	case AgentTypeAI, AgentTypeInTree, AgentTypeCASBinary, AgentTypeExec:
		return true
	default:
		return false
	}
}

func (a AgentType) String() string {
	return string(a)
}

