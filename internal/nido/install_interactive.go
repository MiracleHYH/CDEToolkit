package nido

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// networkOptions is shared by init and bootstrap. It translates the former
// mode flag without keeping a second persisted routing model.
func networkOptions(o cliOptions) (DownloadConfig, bool, error) {
	network := DownloadConfig{GitHubAccelerate: "none", ProxyURL: o.proxy}
	explicit := o.networkMode != "" || o.githubProxy != "" || o.proxy != ""
	if o.githubProxy != "" {
		var err error
		network, err = normalizeDownload(o.githubProxy)
		if err != nil {
			return DownloadConfig{}, false, err
		}
		network.ProxyURL = o.proxy
	}
	if o.networkMode != "" {
		switch strings.ToUpper(o.networkMode) {
		case NetworkDirect:
			if o.githubProxy != "" && !strings.EqualFold(o.githubProxy, "none") && !strings.EqualFold(o.githubProxy, "direct") {
				return DownloadConfig{}, false, fmt.Errorf("DIRECT 与 GitHub Accelerate 选项冲突")
			}
			network.GitHubAccelerate = "none"
		case NetworkGitHubProxy:
			if o.githubProxy == "" {
				network.GitHubAccelerate = "https://gh-proxy.org"
			}
			if network.GitHubAccelerate == "none" || network.GitHubAccelerate == "proxy" {
				return DownloadConfig{}, false, fmt.Errorf("GITHUB_PROXY 需要 HTTPS 加速前缀")
			}
		case NetworkProxy:
			if o.githubProxy != "" && !strings.EqualFold(o.githubProxy, "proxy") {
				return DownloadConfig{}, false, fmt.Errorf("PROXY 与 GitHub Accelerate 选项冲突")
			}
			network.GitHubAccelerate = "proxy"
		default:
			return DownloadConfig{}, false, fmt.Errorf("不支持旧网络模式 %q", o.networkMode)
		}
	}
	network, err := canonicalNetwork(network)
	return network, explicit, err
}

// bootstrapInstall is invoked by the temporary, verified release program.
// The self-contained POSIX bootstrap makes no network requests. Go owns every
// installation decision, including the policy for future downloads.
func (c *CLI) bootstrapInstall(o cliOptions, args []string) int {
	if len(args) != 0 || o.home != "" || o.artifact != "" || o.checksum != "" || o.file != "" || o.url != "" || o.tail != "" || o.tool != "" || o.endpoint != "" || o.check {
		return c.fail(fmt.Errorf("bootstrap-install 仅接受安装目录、GitHub Accelerate、Proxy、--yes 和 --dry-run"))
	}
	if o.json && !o.yes && !o.dry {
		return c.fail(fmt.Errorf("JSON 安装需要 --yes；预览请使用 --dry-run"))
	}
	if o.version != "" && o.version != "latest" && o.version != Version {
		return c.fail(fmt.Errorf("安装器包含 Nido %s；指定其他版本请下载对应标签的 install.sh", Version))
	}
	interactive := !o.yes && !o.dry && !o.json
	network, explicitNetwork, err := networkOptions(o)
	if err != nil {
		return c.fail(err)
	}
	if !explicitNetwork && interactive {
		choice, err := c.choose("GitHub Accelerate（仅 GitHub 文件下载；三条路线互斥）", []string{"None：GitHub 文件直连", "gh-proxy URL：仅拼接加速前缀，不使用 Proxy", "Proxy：引用下方通用代理"}, 0)
		if err != nil {
			return c.fail(err)
		}
		switch choice {
		case 1:
			network.GitHubAccelerate, err = c.setting("GitHub 加速 HTTPS 前缀", "https://gh-proxy.org")
			if err != nil {
				return c.fail(err)
			}
		case 2:
			network.GitHubAccelerate = "proxy"
		}
		choice, err = c.choose("Proxy（Nido 的 API 请求及需要代理的操作）", []string{"None：不配置通用代理", "设置 Proxy URL"}, 0)
		if err != nil {
			return c.fail(err)
		}
		if choice == 1 {
			network.ProxyURL, err = c.secretPrompt("Proxy URL（默认 http://127.0.0.1:7890，隐藏输入）", "http://127.0.0.1:7890")
			if err != nil {
				return c.fail(err)
			}
		}
	}
	if !explicitNetwork && !interactive {
		return c.fail(fmt.Errorf("非交互安装需要明确设置 GitHub Accelerate，例如 --github-accelerate none；Proxy 可独立指定"))
	}
	network, err = canonicalNetwork(network)
	if err != nil {
		return c.fail(err)
	}
	// Production input belongs to the shared Go terminal session. The bootstrap
	// passes /dev/null as stdin so piped shell source is never read as answers.
	root := o.root
	if root == "" {
		root = os.Getenv("NIDO_HOME")
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return c.fail(err)
			}
			root = filepath.Join(home, ".nido")
		}
		if interactive {
			value, err := c.setting("Nido 安装目录", root)
			if err != nil {
				return c.fail(err)
			}
			root = value
		}
	}
	// Clean user-entered trailing slashes here; New remains the single source
	// of directory validation, canonicalisation and ownership checks.
	root = filepath.Clean(root)
	m, err := New(root)
	if err != nil {
		return c.fail(err)
	}
	if m.configBytes == nil {
		if err := m.emptyRoot(); err != nil {
			return c.fail(err)
		}
	}

	shellName, shellPath := "", ""
	if interactive {
		name := filepath.Base(shellEnv(m.Env, "SHELL"))
		if name == "bash" || name == "zsh" {
			path, e := m.shellProfile(name)
			if e == nil {
				answer, e := c.choose("终端自动加载："+path, []string{"暂不修改，安装后手动加载", "启用终端自动加载（写入 Shell 配置）"}, 0)
				if e != nil {
					return c.fail(e)
				}
				if answer == 1 {
					shellName, shellPath = name, path
				}
			} else {
				fmt.Fprintln(c.Err, "Shell 接入路径无效；安装后可手动加载 env.sh。")
			}
		} else {
			fmt.Fprintln(c.Err, "未识别到 Bash/Zsh；安装后显示手动加载命令。")
		}
	}
	transport := networkSummary(network)
	result := map[string]string{"operation": "install", "root": m.Root, "network": transport, "github_accelerate": network.GitHubAccelerate, "proxy": redactedNetworkProxy(network.ProxyURL), "environment": filepath.Join(m.Root, "env.sh"), "activate": ". " + shellQuote(filepath.Join(m.Root, "env.sh"))}
	if o.dry {
		return c.output(o, result)
	}
	if interactive {
		fmt.Fprintf(c.Err, "安装目录：%s\n%s\n", m.Root, transport)
		if shellName == "" {
			fmt.Fprintln(c.Err, "Shell 接入：不修改配置文件")
		} else {
			fmt.Fprintln(c.Err, "Shell 接入："+shellPath)
		}
		if err := c.confirm(o, "安装 Nido（不安装或启动组件）"); err != nil {
			return c.fail(err)
		}
	}
	if err := m.InitWithNetwork(network); err != nil {
		return c.fail(err)
	}
	if shellName != "" {
		plan, err := m.prepareShell(shellName)
		if err == nil {
			err = m.applyShell(plan)
		}
		if err != nil {
			fmt.Fprintln(c.Err, "Nido 已安装，但 Shell 接入未完成；原配置保留，请使用手动加载命令：", err)
		}
	}
	if o.json {
		result["version"] = Version
		return c.output(o, result)
	}
	fmt.Fprintf(c.Out, "Installed nido %s; no components or services were started.\nInstallation directory: %s\n", Version, m.Root)
	fmt.Fprintln(c.Out, "启用当前终端（安装器无法修改父 Shell 环境）：")
	fmt.Fprintln(c.Out, result["activate"])
	return 0
}
