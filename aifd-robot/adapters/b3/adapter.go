package b3

import (
	"context"
	"fmt"
	"sync"

	"github.com/li9chuan/aifirstdev/aifd-robot/behavior"
	robot "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
)

const rootTreeTitle = "root"

// Loader 将外部行为资源转换为框架中立 Project；本包不解析 .b3 文件。
type Loader interface {
	Load(context.Context, string) (Project, error)
}

// LoaderFunc 将函数适配为 Loader。
type LoaderFunc func(context.Context, string) (Project, error)

// Load 调用包装的 loader 函数。
func (f LoaderFunc) Load(ctx context.Context, path string) (Project, error) {
	return f(ctx, path)
}

// Project 是 adapter 使用的格式中立项目表示。
type Project struct {
	Trees []TreeSpec
}

// TreeSpec 描述一棵行为树。
type TreeSpec struct {
	ID    string
	Title string
	Root  robot.BehaviorSpec
}

// TreeStore 保存每个 agent 的行为树实例。
type TreeStore struct {
	mu      sync.RWMutex
	agents  map[string]map[string]*behavior.Tree
	rootIDs map[string]string
}

// NewTreeStore 创建空行为树存储。
func NewTreeStore() *TreeStore {
	return &TreeStore{
		agents:  make(map[string]map[string]*behavior.Tree),
		rootIDs: make(map[string]string),
	}
}

// Attach 为指定 agent 构建独立行为树实例。
func (s *TreeStore) Attach(agent robot.Agent, project Project, registry *behavior.Registry) error {
	if agent == nil {
		return fmt.Errorf("b3 agent is nil")
	}
	if registry == nil {
		return fmt.Errorf("b3 behavior registry is nil")
	}
	trees := make(map[string]*behavior.Tree, len(project.Trees))
	rootID := ""
	for _, spec := range project.Trees {
		if spec.ID == "" {
			return fmt.Errorf("b3 tree id is empty")
		}
		node, err := behavior.Build(spec.Root, registry)
		if err != nil {
			return fmt.Errorf("b3 build tree %s: %w", spec.ID, err)
		}
		trees[spec.ID] = &behavior.Tree{Root: node}
		if spec.Title == rootTreeTitle {
			if rootID != "" {
				return fmt.Errorf("b3 root tree is duplicated")
			}
			rootID = spec.ID
		}
	}
	if rootID == "" {
		return fmt.Errorf("b3 root tree is missing")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[agent.ID()] = trees
	s.rootIDs[agent.ID()] = rootID
	return nil
}

// Tree 查找指定 agent 的行为树。
func (s *TreeStore) Tree(agentID, treeID string) (*behavior.Tree, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	trees := s.agents[agentID]
	if trees == nil {
		return nil, false
	}
	tree, ok := trees[treeID]
	return tree, ok
}

// Root 查找指定 agent 的 root 行为树。
func (s *TreeStore) Root(agentID string) (*behavior.Tree, bool) {
	s.mu.RLock()
	rootID := s.rootIDs[agentID]
	s.mu.RUnlock()
	if rootID == "" {
		return nil, false
	}
	return s.Tree(agentID, rootID)
}
