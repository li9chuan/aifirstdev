package contracts

// TableChangeKind 对表级 manifest diff 结果分类。
type TableChangeKind string

const (
	TableAdded   TableChangeKind = "added"
	TableRemoved TableChangeKind = "removed"
	TableChanged TableChangeKind = "changed"
)

// TableChange 描述单个表级 manifest diff。
type TableChange struct {
	Kind        TableChangeKind
	Table       string
	OldHash     string
	NewHash     string
	OldRowCount int
	NewRowCount int
}

// ChangeSet 描述两个配置包 manifest 之间的轻量变更。
type ChangeSet struct {
	FromVersion  string
	ToVersion    string
	FromDataHash string
	ToDataHash   string

	AddedTables   []string
	RemovedTables []string
	ChangedTables []string
	Tables        []TableChange
}

// ReloadResult 是一次 prepare-then-commit reload 尝试的返回结果。
type ReloadResult struct {
	Applied    bool
	OldVersion string
	NewVersion string
	Issues     []Issue
	ChangeSet  ChangeSet
}
