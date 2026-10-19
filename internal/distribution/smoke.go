package distribution

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Smoke executes only read-only commands in a fresh HOME and software root.
// It never initializes Nido, configures external tools or starts components.
func Smoke(binary []byte, version, platform string) error {
	if platform != runtime.GOOS+"/"+runtime.GOARCH {
		return fmt.Errorf("跨平台包只能静态校验，不能在当前主机执行")
	}
	if err := checkBinary(binary, version, platform); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "nido-dist-smoke-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "nido")
	if err = os.WriteFile(path, binary, 0755); err != nil {
		return err
	}
	home, root := filepath.Join(dir, "home"), filepath.Join(dir, "software")
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "NIDO_HOME=" + root,
		"TMPDIR=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(dir, "cache"), "XDG_DATA_HOME=" + filepath.Join(dir, "data")}
	run := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env, cmd.Dir = env, dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("发布包只读冒烟 %v 失败: %w: %s", args, err, strings.TrimSpace(string(out)))
		}
		return out, nil
	}
	out, err := run("version")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) != "nido "+version {
		return fmt.Errorf("程序报告版本与 VERSION 不符")
	}
	out, err = run("status", "--json")
	if err != nil {
		return err
	}
	var rows []struct {
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		Configured bool   `json:"configured"`
		State      string `json:"state"`
	}
	if json.Unmarshal(out, &rows) != nil || len(rows) != 3 {
		return fmt.Errorf("空环境状态输出无效")
	}
	want := map[string]string{"codex": "external", "conda": "external", "proxy": "builtin"}
	for _, row := range rows {
		if kind, ok := want[row.Name]; !ok || row.Kind != kind || row.Configured || row.State == "" {
			return fmt.Errorf("空环境意外配置了组件或组件分类无效")
		}
		delete(want, row.Name)
	}
	if len(want) != 0 {
		return fmt.Errorf("空环境缺少组件状态")
	}
	if _, err = os.Lstat(root); !os.IsNotExist(err) {
		return fmt.Errorf("只读冒烟创建了软件目录")
	}
	return nil
}
