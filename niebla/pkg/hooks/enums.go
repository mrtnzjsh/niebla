package hooks

// HookType represents the execution mechanism of a validation hook.
type HookType string

const (
	HookTypeMetadata  HookType = "metadata"
	HookTypeTreeScan  HookType = "tree_scan"
	HookTypeCASBinary HookType = "cas_binary"
	HookTypeInTree    HookType = "in_tree"
	HookTypeExec      HookType = "exec"
)

// AllHookTypes lists all valid hook types.
var AllHookTypes = []HookType{
	HookTypeMetadata,
	HookTypeTreeScan,
	HookTypeCASBinary,
	HookTypeInTree,
	HookTypeExec,
}

// IsValid reports whether the hook type is valid.
func (h HookType) IsValid() bool {
	switch h {
	case HookTypeMetadata, HookTypeTreeScan, HookTypeCASBinary, HookTypeInTree, HookTypeExec:
		return true
	default:
		return false
	}
}

func (h HookType) String() string {
	return string(h)
}

