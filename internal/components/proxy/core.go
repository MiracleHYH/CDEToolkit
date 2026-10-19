package proxy

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// Environment is shared by version/config checks and the final launch. Mihomo
// environment overrides must not replace the bytes or policy we validated.
func (s *Service) controlledEnv() []string {
	env := s.Env
	if env == nil {
		env = os.Environ()
	}
	result := make([]string, 0, len(env))
	for _, value := range env {
		key, _, _ := strings.Cut(value, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "CLASH_") || strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "DYLD_") || key == "SAFE_PATHS" || key == "SKIP_SAFE_PATH_CHECK" {
			continue
		}
		result = append(result, value)
	}
	return result
}

// Match a whole whitespace-delimited version token. A word boundary would
// silently truncate suffixes such as "v1.2.4-" or "v1.2.4/other" to a stable
// release tag and weaken the official artifact's version cross-check.
var coreVersionPattern = regexp.MustCompile(`(?i)\bMihomo\s+Meta\s+([A-Za-z0-9.+_-]+)(?:\s|$)`)

func (s *Service) version(program string) (string, error) {
	return s.versionContext(context.Background(), program)
}

func (s *Service) versionContext(ctx context.Context, program string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, _, err := s.paths()
	if err != nil {
		return "", err
	}
	out, err := s.runner().Run(Command{Context: ctx, Program: program, Dir: dir, Args: []string{"-v"}, Env: s.controlledEnv(), Timeout: 10 * time.Second})
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err != nil || len(out) > 8192 {
		return "", fmt.Errorf("候选核心版本检查失败")
	}
	match := coreVersionPattern.FindSubmatch(out)
	if len(match) != 2 {
		return "", fmt.Errorf("候选程序未返回有效的 Mihomo 版本")
	}
	return string(match[1]), nil
}

func (s *Service) validateConfig(core fileSnapshot, data []byte, directory directorySnapshot) error {
	return s.validateConfigContext(context.Background(), core, data, directory)
}

func (s *Service) validateConfigContext(ctx context.Context, core fileSnapshot, data []byte, directory directorySnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, _, err := s.paths()
	if err != nil {
		return err
	}
	if _, err = ParseConfig(data, dir); err != nil {
		return err
	}
	if err = s.requireStopped(); err != nil {
		return fmt.Errorf("核心配置验证需要已停止的 Proxy；运行时只能静态检查：%w", err)
	}
	if err = directory.check(); err != nil {
		return err
	}
	if err = core.check(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	_, err = s.runner().Run(Command{Context: ctx, Program: core.path, Dir: dir, Args: []string{"-t", "-d", dir, "-f", "-"}, Env: s.controlledEnv(), Input: append([]byte(nil), data...), Timeout: 30 * time.Second})
	if err := ctx.Err(); err != nil {
		return err
	}
	if err != nil {
		// Core diagnostics may contain provider URLs, node credentials, and
		// controller secrets. Do not expose unreviewed child output as status.
		return fmt.Errorf("Proxy 核心配置验证失败；主配置未替换（请检查原生配置）")
	}
	return nil
}
