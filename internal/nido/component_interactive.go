package nido

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/MiracleHYH/CDEToolkit/internal/githubdownload"
)

func (c *CLI) codexConfigure(m *Manager, o cliOptions) int {
	if !c.interactive(o) || o.home != "" || o.proxy != "" || o.check {
		return c.fail(fmt.Errorf("codex configure 需要交互终端；脚本请使用 setup --home、install --home 或 proxy URL 并提供 --yes"))
	}
	choice, err := c.choose("Codex：目录和数据由官方工具管理", []string{"保持不变", "关联已有 CODEX_HOME", "在指定外部目录调用官方安装器", "配置 Codex 代理", "解除关联（保留程序和数据）"}, 0)
	if err != nil {
		return c.fail(err)
	}
	if choice == 0 {
		return c.output(o, map[string]string{"detail": "保持 Codex 配置不变"})
	}
	return c.codex(m, o, []string{[]string{"", "setup", "install", "proxy", "detach"}[choice]})
}

// Source selection does not contact the API or infer a proxy binding. Choosing
// an online source still goes through the unchanged API enablement gate.
func (c *CLI) artifactSource(component string, o cliOptions) (cliOptions, bool, error) {
	choice, err := c.choose(component+" 程序来源", []string{"取消，保持不变", "本地已核验的程序包", "在线官方下载（需要已启用的 Nido API 代理）"}, 0)
	if err != nil || choice == 0 {
		return o, true, err
	}
	if choice == 2 {
		return o, false, nil
	}
	o.artifact, err = c.setting("本地程序包的绝对路径", "")
	if err != nil {
		return o, false, err
	}
	if !filepath.IsAbs(o.artifact) {
		return o, false, fmt.Errorf("本地程序包必须是绝对路径")
	}
	if o.checksum == "" {
		o.checksum, err = c.setting("已独立核验的文件 SHA-256（gzip 包填写压缩文件摘要）", "")
	}
	if err == nil && o.checksum == "" {
		err = fmt.Errorf("交互安装本地包需要提供已核验的 SHA-256")
	}
	return o, false, err
}

func (c *CLI) gdsConfiguration(m *Manager, o cliOptions) (cliOptions, bool, error) {
	path, err := m.GDS.ConfigPath()
	if err != nil {
		return o, false, err
	}
	_, err = os.Lstat(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return o, false, err
	}
	options := []string{"保持不变", "导入完整配置文件"}
	if !exists {
		options = append(options, "输入单个 GPU 的完整 UUID，生成默认配置")
	}
	fmt.Fprintln(c.Err, "GDS 配置文件：", path)
	choice, err := c.choose("GDS 为可选组件；配置后保持停止", options, 0)
	if err != nil || choice == 0 {
		return o, true, err
	}
	if choice == 1 {
		o.file, err = c.setting("完整 GDS 配置文件的绝对路径（将整体替换）", "")
		if err == nil && !filepath.IsAbs(o.file) {
			err = fmt.Errorf("配置文件必须是绝对路径")
		}
	} else {
		o.gpu, err = c.setting("GPU 完整 UUID", "")
		if err == nil && o.gpu == "" {
			err = fmt.Errorf("GPU UUID 不能为空")
		}
	}
	return o, false, err
}

func (c *CLI) downloadConfigure(m *Manager, o cliOptions) int {
	if !c.interactive(o) {
		return c.fail(fmt.Errorf("download configure 需要交互终端；脚本请使用 download set VALUE"))
	}
	current := m.Config.Download.GitHubProxy
	if current == "" {
		current = "auto"
	}
	if githubdownload.Validate(current) != nil {
		current = "无效设置"
	}
	fmt.Fprintln(c.Err, "当前 GitHub 文件下载策略：", current, "；不改变 Nido API 绑定或 Codex 代理。")
	choice, err := c.choose("文件下载方式", []string{"保持不变", "auto：官方失败后尝试内置第三方加速", "direct：仅官方地址", "https://gh-proxy.org", "https://ghfast.top", "自定义 HTTPS 文件加速前缀"}, 0)
	if err != nil {
		return c.fail(err)
	}
	if choice == 0 {
		return c.output(o, map[string]string{"detail": "保持下载策略不变"})
	}
	value := []string{"", "auto", "direct", "https://gh-proxy.org", "https://ghfast.top", ""}[choice]
	if choice == 5 {
		value, err = c.setting("HTTPS 文件加速前缀", "")
		if err != nil {
			return c.fail(err)
		}
		if value == "" {
			return c.fail(fmt.Errorf("自定义 HTTPS 文件加速前缀不能为空"))
		}
	}
	if err := githubdownload.Validate(value); err != nil {
		return c.fail(err)
	}
	if err := c.confirm(o, "保存 GitHub 文件下载策略 "+value); err != nil {
		return c.fail(err)
	}
	return c.download(m, o, []string{"set", value})
}
