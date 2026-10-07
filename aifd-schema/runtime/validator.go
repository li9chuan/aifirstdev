package runtime

import (
	"sort"

	schema "github.com/li9chuan/aifirstdev/aifd-schema/contracts"
)

// ChangeSeverity 对 schema 兼容性变更分类。
type ChangeSeverity string

const (
	ChangeInfo     ChangeSeverity = "info"
	ChangeWarning  ChangeSeverity = "warning"
	ChangeBreaking ChangeSeverity = "breaking"
)

// Change 描述一条兼容性检查发现。
type Change struct {
	Severity ChangeSeverity
	Message  string
	Field    string
	Reason   string
}

// CompatibilityReport 是比较两个 schema 快照得到的结果。
type CompatibilityReport struct {
	Changes []Change
}

// HasBreaking 表示是否存在需要大版本或显式迁移计划的变更。
func (r CompatibilityReport) HasBreaking() bool {
	for _, change := range r.Changes {
		if change.Severity == ChangeBreaking {
			return true
		}
	}
	return false
}

// CheckCompatibility 比较旧版和当前描述；这里只检查稳定 schema 事实，项目专属 proto 风格规则放在 aifd-rules。
func CheckCompatibility(previous, current []schema.MessageDescriptor) CompatibilityReport {
	prevByName := descriptorsByName(previous)
	currByName := descriptorsByName(current)
	names := make([]string, 0, len(prevByName))
	for name := range prevByName {
		names = append(names, name)
	}
	sort.Strings(names)

	var report CompatibilityReport
	for _, name := range names {
		prev := prevByName[name]
		curr, ok := currByName[name]
		if !ok {
			report.add(ChangeBreaking, name, "", "message removed")
			continue
		}
		checkMessageCompatibility(&report, prev, curr)
	}
	for _, name := range sortedNewNames(prevByName, currByName) {
		report.add(ChangeInfo, name, "", "message added")
	}
	return report
}

func checkMessageCompatibility(report *CompatibilityReport, prev, curr schema.MessageDescriptor) {
	if prev.ID != 0 && curr.ID != prev.ID {
		report.add(ChangeBreaking, prev.Name, "", "message id changed")
	}
	if prev.Direction != "" && curr.Direction != prev.Direction {
		report.add(ChangeBreaking, prev.Name, "", "message direction changed")
	}
	prevFields := fieldsByNumber(prev.Fields)
	currFields := fieldsByNumber(curr.Fields)
	numbers := make([]int, 0, len(prevFields))
	for number := range prevFields {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	for _, number := range numbers {
		oldField := prevFields[number]
		newField, ok := currFields[number]
		if !ok {
			report.add(ChangeBreaking, prev.Name, oldField.Name, "field removed")
			continue
		}
		if oldField.Type != newField.Type || oldField.Repeated != newField.Repeated {
			report.add(ChangeBreaking, prev.Name, oldField.Name, "field wire shape changed")
		}
		if oldField.Optional && !newField.Optional {
			report.add(ChangeBreaking, prev.Name, oldField.Name, "optional field became required")
		}
		if oldField.Name != newField.Name {
			report.add(ChangeWarning, prev.Name, newField.Name, "field renamed")
		}
	}
	for _, number := range sortedNewFieldNumbers(prevFields, currFields) {
		field := currFields[number]
		severity := ChangeInfo
		reason := "optional field added"
		if !field.Optional {
			severity = ChangeWarning
			reason = "non-optional field added"
		}
		report.add(severity, prev.Name, field.Name, reason)
	}
}

func (r *CompatibilityReport) add(severity ChangeSeverity, message, field, reason string) {
	r.Changes = append(r.Changes, Change{
		Severity: severity,
		Message:  message,
		Field:    field,
		Reason:   reason,
	})
}

func descriptorsByName(descs []schema.MessageDescriptor) map[string]schema.MessageDescriptor {
	out := make(map[string]schema.MessageDescriptor, len(descs))
	for _, desc := range descs {
		if desc.Name != "" {
			out[desc.Name] = desc
		}
	}
	return out
}

func fieldsByNumber(fields []schema.FieldDescriptor) map[int]schema.FieldDescriptor {
	out := make(map[int]schema.FieldDescriptor, len(fields))
	for _, field := range fields {
		if field.Number > 0 {
			out[field.Number] = field
		}
	}
	return out
}

func sortedNewNames(previous, current map[string]schema.MessageDescriptor) []string {
	names := make([]string, 0)
	for name := range current {
		if _, exists := previous[name]; !exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func sortedNewFieldNumbers(previous, current map[int]schema.FieldDescriptor) []int {
	numbers := make([]int, 0)
	for number := range current {
		if _, exists := previous[number]; !exists {
			numbers = append(numbers, number)
		}
	}
	sort.Ints(numbers)
	return numbers
}
