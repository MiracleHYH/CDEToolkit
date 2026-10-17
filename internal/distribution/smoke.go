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

// Smoke runs only the verified native package, with isolated HOME and STK_HOME.
// The two read-only commands never install components or start services.
func Smoke(binary []byte, version, platform string) error {
	if platform != runtime.GOOS+"/"+runtime.GOARCH {
		return fmt.Errorf("跨平台包只能静态校验，不能在当前主机执行")
	}
	if err := checkBinary(binary, version, platform); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "stk-dist-smoke-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "stk")
	if err = os.WriteFile(path, binary, 0755); err != nil {
		return err
	}
	home := filepath.Join(dir, "home")
	managed := filepath.Join(dir, "managed")
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "STK_HOME=" + managed,
		"TMPDIR=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(dir, "cache"), "XDG_DATA_HOME=" + filepath.Join(dir, "data")}
	run := func(args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, args...)
		cmd.Env = env
		cmd.Dir = dir
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
	if strings.TrimSpace(string(out)) != "stk-go "+version {
		return fmt.Errorf("程序报告版本与 VERSION 不符")
	}
	out, err = run("status", "--json")
	if err != nil {
		return err
	}
	var rows []struct {
		Module  string `json:"module"`
		Managed bool   `json:"managed"`
	}
	if json.Unmarshal(out, &rows) != nil || len(rows) != 3 {
		return fmt.Errorf("空环境状态输出无效")
	}
	for _, row := range rows {
		if row.Managed {
			return fmt.Errorf("空环境意外登记了组件")
		}
	}
	if _, err = os.Lstat(managed); !os.IsNotExist(err) {
		return fmt.Errorf("只读冒烟创建了管理目录")
	}
	return nil
}
