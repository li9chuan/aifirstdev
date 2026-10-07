package contracts

import "time"

// Bundle 是一次已加载的强类型配置包。
//
// Data 由业务项目拥有，例如生成的 cfgtable 或业务组合出的 AppConfigBundle。
// aifd-config 不复制 Data；Loader 返回后调用方必须将 Data 视为不可变对象。
type Bundle[T any] struct {
	Version  string    `json:"version"`
	LoadedAt time.Time `json:"loaded_at,omitempty"`
	Data     T         `json:"data"`
	Manifest Manifest  `json:"manifest,omitempty"`
}

// Manifest 是配置包的轻量控制面视图，用于审计、diff、observe 和影响面报告。
// 它不是业务配置真相，也不要求覆盖每一张业务表。
type Manifest struct {
	DataHash   string                    `json:"data_hash,omitempty"`
	SchemaHash string                    `json:"schema_hash,omitempty"`
	Profile    string                    `json:"profile,omitempty"`
	Tables     map[string]TableManifest  `json:"tables,omitempty"`
	Sources    map[string]SourceManifest `json:"sources,omitempty"`
	Labels     map[string]string         `json:"labels,omitempty"`
}

// TableManifest 描述一张表的轻量审计信息。
type TableManifest struct {
	Name     string `json:"name,omitempty"`
	RowCount int    `json:"row_count,omitempty"`
	Hash     string `json:"hash,omitempty"`
}

// SourceManifest 描述配置包输入源的轻量审计信息。
type SourceManifest struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
	Hash string `json:"hash,omitempty"`
	Kind string `json:"kind,omitempty"`
}

// Clone 返回 Manifest 的独立拷贝。
func (m Manifest) Clone() Manifest {
	out := m
	if len(m.Tables) > 0 {
		out.Tables = make(map[string]TableManifest, len(m.Tables))
		for name, table := range m.Tables {
			out.Tables[name] = table
		}
	}
	if len(m.Sources) > 0 {
		out.Sources = make(map[string]SourceManifest, len(m.Sources))
		for name, source := range m.Sources {
			out.Sources[name] = source
		}
	}
	if len(m.Labels) > 0 {
		out.Labels = make(map[string]string, len(m.Labels))
		for key, value := range m.Labels {
			out.Labels[key] = value
		}
	}
	return out
}
