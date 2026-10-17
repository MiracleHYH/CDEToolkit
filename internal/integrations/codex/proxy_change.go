package codex

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// proxyContent changes only the owned block in a single captured .env snapshot.
func proxyContent(c Config, file envFile, endpoint string) (Config, []byte, error) {
	other, _, err := stripOwned(file.content, c.ProxyBlock)
	if err != nil {
		return c, nil, err
	}
	if endpoint == "" || endpoint == "off" {
		c.ProxyBlock = ""
		return c, other, nil
	}
	if err := validProxy(endpoint); err != nil {
		return c, nil, err
	}
	assignments, err := dotenvProxy(other)
	if err != nil {
		return c, nil, err
	}
	for key := range assignments {
		if key != "no_proxy" {
			return c, nil, fmt.Errorf(".env 已有非 Nido 管理的代理变量；请先明确处理冲突，未修改文件")
		}
	}
	block := ""
	if len(other) > 0 && other[len(other)-1] != '\n' {
		block = "\n"
	}
	block += proxyStart + "\n"
	for _, key := range proxyKeys {
		block += key + "='" + endpoint + "'\n"
	}
	block += proxyEnd + "\n"
	c.ProxyBlock = block
	return c, append(append([]byte(nil), other...), []byte(block)...), nil
}

// CheckProxy validates the proposed edit without creating files or changing
// the external home. It shares the exact conflict checks with ChangeProxy.
func (s Service) CheckProxy(c Config, endpoint string) error {
	home, err := s.home(c.Home, true)
	if err != nil {
		return err
	}
	before, err := readEnv(home)
	if err != nil {
		return err
	}
	_, next, err := proxyContent(c, before, endpoint)
	if err != nil {
		return err
	}
	if len(next) > maxEnvSize {
		return fmt.Errorf("添加代理配置后 .env 将超过 1 MiB，未修改文件")
	}
	return nil
}

// ChangeProxy returns a short-lived compensation callback for a failed first
// configuration save. It keeps no journal and only touches this one .env file.
// Once the caller has saved the new receipt, it must discard the callback.
func (s Service) ChangeProxy(c Config, endpoint string) (Config, func() error, error) {
	home, err := s.home(c.Home, true)
	if err != nil {
		return c, nil, err
	}
	c.Home = home
	before, err := readEnv(home)
	if err != nil {
		return c, nil, err
	}
	next, expected, err := proxyContent(c, before, endpoint)
	if err != nil {
		return c, nil, err
	}
	if len(expected) > maxEnvSize {
		return c, nil, fmt.Errorf("添加代理配置后 .env 将超过 1 MiB，未修改文件")
	}
	if err := writeEnv(home, before, expected); err != nil {
		return c, nil, err
	}
	if bytes.Equal(before.content, expected) {
		return next, func() error { return nil }, nil
	}
	// The write succeeded, so return its ownership receipt even if a user
	// immediately changed the file again. In that case compensation refuses
	// to guess; the caller can still persist the receipt and show the conflict.
	after, snapshotErr := readEnv(home)
	if snapshotErr == nil && !bytes.Equal(after.content, expected) {
		snapshotErr = fmt.Errorf("Codex .env 在代理修改后又发生变化")
	}
	var mu sync.Mutex
	done := false
	undo := func() error {
		mu.Lock()
		defer mu.Unlock()
		if done {
			return nil
		}
		if snapshotErr != nil || after.info == nil {
			return fmt.Errorf("Codex .env 在代理修改后又发生变化，未恢复")
		}
		actualHome, err := s.home(home, true)
		if err != nil || actualHome != home {
			return fmt.Errorf("Codex 目录已变化，未恢复代理配置")
		}
		current, err := readEnv(home)
		if err != nil || current.info == nil || after.dir == nil || current.dir == nil || !os.SameFile(after.dir, current.dir) || !os.SameFile(after.info, current.info) || !bytes.Equal(expected, current.content) || after.info.Mode().Perm() != current.info.Mode().Perm() || !after.info.ModTime().Equal(current.info.ModTime()) {
			return fmt.Errorf("Codex .env 在代理修改后又发生变化，未恢复")
		}
		if before.info == nil {
			if err = os.Remove(filepath.Join(home, ".env")); err != nil {
				return fmt.Errorf("无法移除本次新建的 Codex .env")
			}
		} else if err = writeEnv(home, current, before.content); err != nil {
			return err
		}
		done = true
		return nil
	}
	return next, undo, nil
}
