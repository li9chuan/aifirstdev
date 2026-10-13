package contracts

// ServiceRole 标识拥有 Module 或 Route 的进程边界。
type ServiceRole string

const (
	ServiceRoleAccount ServiceRole = "account"
	ServiceRoleTool    ServiceRole = "tool"
)

// ModuleKind 标识 Module 在 runtime 中的承载方式。
type ModuleKind string

const (
	ModuleKindUser    ModuleKind = "user"
	ModuleKindService ModuleKind = "service"
	ModuleKindAdapter ModuleKind = "adapter"
)

// Capability 描述 Module 对外提供的公共能力面。
type Capability struct {
	Kind string
	Name string
}

// ModuleDescriptor 是跨项目共享的稳定最小 Module 描述。
type ModuleDescriptor struct {
	ID       string
	Version  string
	Kind     ModuleKind
	Depends  []string
	Provides []Capability
}

// Validate 只检查框架级 descriptor 不变量。
func (d ModuleDescriptor) Validate() error {
	if d.ID == "" {
		return ErrEmptyModuleID
	}
	if d.Kind == "" {
		return ErrEmptyModuleKind
	}
	return nil
}
