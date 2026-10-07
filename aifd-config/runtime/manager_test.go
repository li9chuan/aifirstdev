package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-config/adapters/memory"
	config "github.com/li9chuan/aifirstdev/aifd-config/contracts"
)

type testConfig struct {
	Limit int
}

func TestManagerReloadCommitsTypedBundleAndReportsManifestDiff(t *testing.T) {
	loader := memory.NewLoader(testBundle("v1", "data:v1", map[string]config.TableManifest{
		"Item": {Name: "Item", RowCount: 1, Hash: "item:coin"},
	}, 1))
	manager, err := NewManager(loader)
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	result, err := manager.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload error = %v", err)
	}
	if !result.Applied {
		t.Fatalf("first reload not applied: %+v", result)
	}

	loader.SetBundle(testBundle("v2", "data:v2", map[string]config.TableManifest{
		"Item":  {Name: "Item", RowCount: 1, Hash: "item:gold"},
		"Skill": {Name: "Skill", RowCount: 1, Hash: "skill:slash"},
	}, 2))
	result, err = manager.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload v2 error = %v", err)
	}
	if !result.Applied {
		t.Fatalf("second reload not applied: %+v", result)
	}
	if result.ChangeSet.FromVersion != "v1" || result.ChangeSet.ToVersion != "v2" {
		t.Fatalf("change set versions = %+v", result.ChangeSet)
	}
	if result.ChangeSet.FromDataHash != "data:v1" || result.ChangeSet.ToDataHash != "data:v2" {
		t.Fatalf("change set data hash = %+v", result.ChangeSet)
	}
	if len(result.ChangeSet.AddedTables) != 1 || result.ChangeSet.AddedTables[0] != "Skill" {
		t.Fatalf("added tables = %+v, want Skill", result.ChangeSet.AddedTables)
	}
	if len(result.ChangeSet.ChangedTables) != 1 || result.ChangeSet.ChangedTables[0] != "Item" {
		t.Fatalf("changed tables = %+v, want Item", result.ChangeSet.ChangedTables)
	}
	current, ok := manager.Current()
	if !ok || current.Data.Limit != 2 {
		t.Fatalf("current bundle = %+v ok=%v, want limit 2", current, ok)
	}
}

func TestManagerReloadPreparesHooksBeforeCommit(t *testing.T) {
	loader := memory.NewLoader(testBundle("v1", "data:v1", map[string]config.TableManifest{
		"Item": {Name: "Item", RowCount: 1, Hash: "item:coin"},
	}, 1))
	manager, err := NewManager(loader)
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	var order []string
	if err := manager.RegisterHook(Hook[testConfig]{
		Name: "first",
		Prepare: func(_ context.Context, next *config.Bundle[testConfig]) (ApplyFunc, error) {
			if next.Version != "v1" {
				t.Fatalf("hook bundle version = %s, want v1", next.Version)
			}
			order = append(order, "prepare:first")
			return func() { order = append(order, "apply:first") }, nil
		},
	}); err != nil {
		t.Fatalf("RegisterHook first error = %v", err)
	}
	if err := manager.RegisterHook(Hook[testConfig]{
		Name: "second",
		Prepare: func(context.Context, *config.Bundle[testConfig]) (ApplyFunc, error) {
			order = append(order, "prepare:second")
			return func() { order = append(order, "apply:second") }, nil
		},
	}); err != nil {
		t.Fatalf("RegisterHook second error = %v", err)
	}
	result, err := manager.Reload(context.Background())
	if err != nil || !result.Applied {
		t.Fatalf("Reload result = %+v err = %v", result, err)
	}
	want := []string{"prepare:first", "prepare:second", "apply:first", "apply:second"}
	if !equalStringSlices(order, want) {
		t.Fatalf("hook order = %v, want %v", order, want)
	}
}

func TestManagerReloadHookFailureKeepsOldBundle(t *testing.T) {
	loader := memory.NewLoader(testBundle("v1", "data:v1", map[string]config.TableManifest{
		"Item": {Name: "Item", RowCount: 1, Hash: "item:coin"},
	}, 1))
	manager, err := NewManager(loader)
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	if result, err := manager.Reload(context.Background()); err != nil || !result.Applied {
		t.Fatalf("initial Reload result = %+v err = %v", result, err)
	}

	loader.SetBundle(testBundle("v2", "data:v2", map[string]config.TableManifest{
		"Item": {Name: "Item", RowCount: 1, Hash: "item:gold"},
	}, 2))
	hookErr := errors.New("derived cache broken")
	applied := false
	if err := manager.RegisterHook(Hook[testConfig]{
		Name: "broken",
		Prepare: func(context.Context, *config.Bundle[testConfig]) (ApplyFunc, error) {
			return func() { applied = true }, hookErr
		},
	}); err != nil {
		t.Fatalf("RegisterHook error = %v", err)
	}
	result, err := manager.Reload(context.Background())
	if !errors.Is(err, hookErr) {
		t.Fatalf("Reload error = %v, want %v", err, hookErr)
	}
	if result.Applied {
		t.Fatalf("reload applied after hook failure: %+v", result)
	}
	if applied {
		t.Fatal("hook apply ran after prepare failure")
	}
	current, ok := manager.Current()
	if !ok || current.Version != "v1" || current.Data.Limit != 1 {
		t.Fatalf("current bundle = %+v ok=%v, want v1", current, ok)
	}
}

func TestHookManagerDuplicateRegistrationIsIdempotent(t *testing.T) {
	manager := NewHookManager[testConfig]()
	calls := 0
	hook := Hook[testConfig]{
		Name: "same",
		Prepare: func(context.Context, *config.Bundle[testConfig]) (ApplyFunc, error) {
			calls++
			return nil, nil
		},
	}
	if err := manager.Register(hook); err != nil {
		t.Fatalf("Register first error = %v", err)
	}
	if err := manager.Register(hook); err != nil {
		t.Fatalf("Register second error = %v", err)
	}
	if _, err := manager.prepare(context.Background(), testBundle("v1", "data:v1", nil, 1)); err != nil {
		t.Fatalf("prepare error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("prepare calls = %d, want 1", calls)
	}
}

func TestManagerReloadRejectsErrorIssuesAndKeepsOldBundle(t *testing.T) {
	loader := memory.NewLoader(testBundle("v1", "data:v1", map[string]config.TableManifest{
		"Item": {Name: "Item", RowCount: 1, Hash: "item:coin"},
	}, 1))
	manager, err := NewManager(loader, config.ValidatorFunc[testConfig](func(_ context.Context, bundle *config.Bundle[testConfig]) []config.Issue {
		if bundle.Data.Limit > 1 {
			return []config.Issue{{Severity: config.IssueError, Table: "Item", Message: "limit too high"}}
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	if result, err := manager.Reload(context.Background()); err != nil || !result.Applied {
		t.Fatalf("initial reload result = %+v err = %v", result, err)
	}

	loader.SetBundle(testBundle("v2", "data:v2", map[string]config.TableManifest{
		"Item": {Name: "Item", RowCount: 1, Hash: "item:gold"},
	}, 2))
	result, err := manager.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload with issue error = %v", err)
	}
	if result.Applied {
		t.Fatalf("reload applied with error issue: %+v", result)
	}
	current, ok := manager.Current()
	if !ok || current.Version != "v1" || current.Data.Limit != 1 {
		t.Fatalf("current bundle = %+v ok=%v, want v1", current, ok)
	}
}

func TestBasicValidatorRejectsBrokenManifest(t *testing.T) {
	loader := memory.NewLoader(testBundle("bad", "data:bad", map[string]config.TableManifest{
		"Item": {Name: "Other", RowCount: -1, Hash: "broken"},
	}, 1))
	manager, err := NewManager(loader)
	if err != nil {
		t.Fatalf("NewManager error = %v", err)
	}
	result, err := manager.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload error = %v", err)
	}
	if result.Applied {
		t.Fatalf("reload applied with broken manifest: %+v", result)
	}
	if len(result.Issues) != 2 {
		t.Fatalf("issues = %+v, want 2 errors", result.Issues)
	}
	if _, ok := manager.Current(); ok {
		t.Fatal("current bundle exists after rejected initial reload")
	}
}

func testBundle(version, dataHash string, tables map[string]config.TableManifest, limit int) *config.Bundle[testConfig] {
	return &config.Bundle[testConfig]{
		Version: version,
		Data:    testConfig{Limit: limit},
		Manifest: config.Manifest{
			DataHash: dataHash,
			Tables:   tables,
		},
	}
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
