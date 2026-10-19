package nido

import (
	"context"
	"fmt"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/components/proxy"
)

// The public UI supplies the current task context. Explicit CLI calls retain
// the service's bounded requests without introducing a component UI runtime.
func (c *CLI) proxyTaskContext() context.Context {
	if c.uiSession != nil {
		return c.uiSession.Context()
	}
	if task, ok := c.UI.(interface{ Context() context.Context }); ok {
		if ctx := task.Context(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

type proxyProviderResult struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

func (c *CLI) proxyProviderUpdate(m *Manager, o cliOptions, args []string) int {
	fail := func(err error) int { return c.proxyError(o, err) }
	if o.dry {
		return c.output(o, map[string]string{"operation": "provider-update", "detail": "未请求或刷新 provider；提交时需要运行中的受管 controller"})
	}
	if m.configBytes == nil {
		return fail(fmt.Errorf("尚未初始化，请先运行 nido init"))
	}
	if len(args) == 0 && !c.interactive(o) {
		return fail(fmt.Errorf("provider-update 需要明确指定 all 或 proxy|rule NAME"))
	}
	ctx := c.proxyTaskContext()
	providers, err := m.Proxy.ProvidersContext(ctx)
	if err != nil {
		return fail(err)
	}
	refreshable := make([]proxy.ProviderInfo, 0, len(providers))
	for _, provider := range providers {
		if strings.EqualFold(provider.VehicleType, "http") || strings.EqualFold(provider.VehicleType, "file") {
			refreshable = append(refreshable, provider)
		}
	}
	providers = refreshable
	if len(providers) == 0 {
		if len(args) == 2 {
			return fail(fmt.Errorf("未找到指定的可刷新 provider"))
		}
		return c.output(o, []proxyProviderResult{})
	}
	selected := []proxy.ProviderInfo{}
	if len(args) == 0 {
		options := []string{"取消，保持不变", "刷新全部已列出的 provider"}
		for _, provider := range providers {
			options = append(options, provider.Kind+" / "+provider.Name)
		}
		choice, err := c.choose("选择要手动刷新的 provider", options, 0)
		if err != nil {
			return fail(err)
		}
		if choice == 0 {
			return c.output(o, map[string]string{"detail": "未刷新 provider"})
		}
		if choice == 1 {
			selected = providers
		} else {
			selected = append(selected, providers[choice-2])
		}
	} else if len(args) == 1 && args[0] == "all" {
		selected = providers
	} else {
		for _, provider := range providers {
			if provider.Kind == args[0] && provider.Name == args[1] {
				selected = append(selected, provider)
			}
		}
		if len(selected) == 0 {
			return fail(fmt.Errorf("未找到指定 provider"))
		}
	}
	if err := c.confirm(o, fmt.Sprintf("由 core 手动刷新 %d 个 provider，逐项报告结果", len(selected))); err != nil {
		return fail(err)
	}
	results := make([]proxyProviderResult, 0, len(selected))
	failed := false
	err = m.mutate(false, func() error {
		for _, provider := range selected {
			result := proxyProviderResult{Kind: provider.Kind, Name: provider.Name, State: "updated"}
			if ctx.Err() != nil {
				result.State, result.Error, failed = "not_attempted", "操作已取消；未发起刷新", true
			} else if err := m.Proxy.RefreshProviderContext(ctx, provider.Kind, provider.Name); err != nil {
				result.State, result.Error, failed = "failed", err.Error(), true
			}
			results = append(results, result)
		}
		return nil
	})
	if err != nil {
		return fail(err)
	}
	if code := c.output(o, results); code != 0 {
		return code
	}
	if failed {
		return 1
	}
	return 0
}
