package runtime

import (
	"context"

	config "github.com/li9chuan/aifirstdev/aifd-config/contracts"
)

// BasicValidator 检查框架级配置包不变量。
type BasicValidator[T any] struct{}

// Validate 检查配置包和 manifest 的身份一致性。
func (BasicValidator[T]) Validate(_ context.Context, bundle *config.Bundle[T]) []config.Issue {
	if bundle == nil {
		return []config.Issue{{Severity: config.IssueError, Message: "config bundle is nil"}}
	}
	var issues []config.Issue
	for tableName, table := range bundle.Manifest.Tables {
		if tableName == "" {
			issues = append(issues, config.Issue{Severity: config.IssueError, Message: "table name is empty"})
			continue
		}
		if table.Name != "" && table.Name != tableName {
			issues = append(issues, config.Issue{
				Severity: config.IssueError,
				Table:    tableName,
				Message:  "table key and table name differ",
			})
		}
		if table.RowCount < 0 {
			issues = append(issues, config.Issue{
				Severity: config.IssueError,
				Table:    tableName,
				Message:  "table row count is negative",
			})
		}
	}
	return issues
}

func hasErrorIssue(issues []config.Issue) bool {
	for _, issue := range issues {
		if issue.Severity == config.IssueError {
			return true
		}
	}
	return false
}
