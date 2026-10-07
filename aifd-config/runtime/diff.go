package runtime

import (
	"sort"

	config "github.com/li9chuan/aifirstdev/aifd-config/contracts"
)

// Diff 比较两个配置包的 manifest，并返回稳定的轻量变更集。
func Diff[T any](previous, current *config.Bundle[T]) config.ChangeSet {
	changes := config.ChangeSet{}
	if previous != nil {
		changes.FromVersion = previous.Version
		changes.FromDataHash = previous.Manifest.DataHash
	}
	if current != nil {
		changes.ToVersion = current.Version
		changes.ToDataHash = current.Manifest.DataHash
	}

	prevTables := manifestTables(previous)
	currTables := manifestTables(current)
	for _, name := range sortedAddedTables(prevTables, currTables) {
		table := currTables[name]
		changes.AddedTables = append(changes.AddedTables, name)
		changes.Tables = append(changes.Tables, config.TableChange{
			Kind:        config.TableAdded,
			Table:       name,
			NewHash:     table.Hash,
			NewRowCount: table.RowCount,
		})
	}
	for _, name := range sortedRemovedTables(prevTables, currTables) {
		table := prevTables[name]
		changes.RemovedTables = append(changes.RemovedTables, name)
		changes.Tables = append(changes.Tables, config.TableChange{
			Kind:        config.TableRemoved,
			Table:       name,
			OldHash:     table.Hash,
			OldRowCount: table.RowCount,
		})
	}
	for _, name := range sortedCommonTables(prevTables, currTables) {
		prev := prevTables[name]
		curr := currTables[name]
		if prev.Hash != curr.Hash || prev.RowCount != curr.RowCount || prev.Name != curr.Name {
			changes.ChangedTables = append(changes.ChangedTables, name)
			changes.Tables = append(changes.Tables, config.TableChange{
				Kind:        config.TableChanged,
				Table:       name,
				OldHash:     prev.Hash,
				NewHash:     curr.Hash,
				OldRowCount: prev.RowCount,
				NewRowCount: curr.RowCount,
			})
		}
	}
	return changes
}

func manifestTables[T any](bundle *config.Bundle[T]) map[string]config.TableManifest {
	if bundle == nil || bundle.Manifest.Tables == nil {
		return nil
	}
	return bundle.Manifest.Tables
}

func sortedAddedTables(previous, current map[string]config.TableManifest) []string {
	names := make([]string, 0)
	for name := range current {
		if _, exists := previous[name]; !exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func sortedRemovedTables(previous, current map[string]config.TableManifest) []string {
	names := make([]string, 0)
	for name := range previous {
		if _, exists := current[name]; !exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func sortedCommonTables(previous, current map[string]config.TableManifest) []string {
	names := make([]string, 0)
	for name := range current {
		if _, exists := previous[name]; exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
