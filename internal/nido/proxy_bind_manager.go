package nido

import (
	"bytes"
	"fmt"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
)

// The CLI orchestrates independent integrations. Proxy does not depend on Git,
// Codex or category configuration, and categories have no background inheritance.
func (m *Manager) checkToolProxy(tool, endpoint string) error {
	switch tool {
	case "nido":
		if _, _, err := m.readAPIProxy(); err != nil {
			return err
		}
		if endpoint == "" {
			return nil
		}
		return validateBindingProxy(endpoint)
	case "codex":
		return m.Codex.CheckProxy(m.Config.Codex, endpoint)
	case "git":
		return m.Git.CheckProxy(m.Config.Git, endpoint)
	default:
		return fmt.Errorf("不支持的代理配置工具")
	}
}

func (m *Manager) configureToolProxy(tool, endpoint string, expected *proxy.Endpoint) error {
	if tool == "nido" {
		return m.configureAPIProxy(endpoint, expected)
	}
	if tool == "codex" {
		return m.configureProxy(endpoint, false, expected)
	}
	if tool != "git" {
		return fmt.Errorf("不支持的代理配置工具")
	}
	return m.mutate(false, func() error {
		if err := m.checkBuiltinEndpoint(expected); err != nil {
			return err
		}
		before := m.Config
		priorBytes := append([]byte(nil), m.configBytes...)
		next, undo, err := m.Git.ChangeProxy(m.Config.Git, endpoint)
		if err != nil {
			return err
		}
		if err = m.checkBuiltinEndpoint(expected); err != nil {
			if undoErr := undo(); undoErr != nil {
				return fmt.Errorf("%w；Git 配置已变化，无法恢复本次修改: %v", err, undoErr)
			}
			return err
		}
		m.Config.Git = next
		if err = m.save(); err != nil && bytes.Equal(priorBytes, m.configBytes) {
			m.Config = before
			if undoErr := undo(); undoErr != nil {
				return fmt.Errorf("%w；Git 配置已变化，无法恢复本次修改: %v", err, undoErr)
			}
		}
		return err
	})
}
