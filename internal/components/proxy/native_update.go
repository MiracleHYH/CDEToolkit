package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func (s *Service) Reload() error { return s.ReloadContext(context.Background()) }

// Reload applies the fixed on-disk configuration through the already managed
// core. The configuration/controller endpoint may not change during the call.
// A 204 acknowledges ApplyConfig, not complete provider/upstream health.
func (s *Service) ReloadContext(ctx context.Context) error {
	return s.withLock(func(directory directorySnapshot) error {
		c, err := s.controllerSession(true, true)
		if err != nil {
			return err
		}
		defer c.close()
		if err = directory.check(); err != nil {
			return err
		}
		body, err := json.Marshal(struct {
			Path string `json:"path"`
		}{c.configFile.path})
		if err != nil {
			return err
		}
		if _, err = c.request(ctx, http.MethodPut, "/configs?force=true", body, http.StatusNoContent, true, true); err != nil {
			return err
		}
		return c.observe(ctx, directory, false, "")
	})
}

type runningImageObserver interface {
	RunningImageMatches(Process, string) (bool, error)
}

func (s *Service) Upgrade() error { return s.UpgradeContext(context.Background()) }

// Upgrade delegates network/version choice to Mihomo's native release updater.
// Nido does not forward its own download policy or claim an independent asset
// digest verification. Only this explicitly requested same-birth transition is
// eligible to refresh the program digest in the process record.
func (s *Service) UpgradeContext(ctx context.Context) error {
	if s.platform() != "linux/amd64" && s.platform() != "linux/arm64" {
		return fmt.Errorf("原生 Proxy 更新的运行映像确认仅支持 Linux amd64/arm64")
	}
	if _, ok := s.runner().(runningImageObserver); !ok {
		return fmt.Errorf("当前运行器不支持 Linux 运行映像确认；未请求更新")
	}
	return s.withLock(func(directory directorySnapshot) error {
		c, err := s.controllerSession(false, true)
		if err != nil {
			return err
		}
		defer c.close()
		if err = directory.check(); err != nil {
			return err
		}
		if err = nativeCoreSafety(c.core); err != nil {
			return err
		}
		if err = s.nativeUpdatePaths(); err != nil {
			return err
		}
		version, err := c.version(ctx, false)
		if err != nil {
			return err
		}
		// The upstream updater recursively removes meta-update even after a
		// failed mkdir. Recheck its absence immediately before this request.
		if err = s.nativeUpdatePaths(); err != nil {
			return err
		}
		data, err := c.request(ctx, http.MethodPost, "/upgrade?channel=release", nil, http.StatusOK, true, true)
		if err != nil {
			return err
		}
		var result struct {
			Status string `json:"status"`
		}
		if decodeController(data, &result) != nil || result.Status != "ok" {
			return fmt.Errorf("原生更新响应无效；结果未确认，未自动重试")
		}
		return c.observe(ctx, directory, true, version)
	})
}

func nativeCoreSafety(file fileSnapshot) error {
	if file.info == nil {
		return fmt.Errorf("原生核心文件不存在")
	}
	owner, ok := file.info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || file.info.Mode().Perm()&0022 != 0 || file.info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fmt.Errorf("原生核心文件必须归本用户所有且无特殊权限、不可由其他用户写入")
	}
	return nil
}

// Native updater directories remain core-owned. We neither clean them nor
// invent a backup catalogue. Repeated native updates can overwrite the single
// native backup; unexpected files, links and ownership fail before the POST.
func (s *Service) nativeUpdatePaths() error {
	dir, _, err := s.paths()
	if err != nil {
		return err
	}
	update := filepath.Join(dir, "meta-update")
	if err = safePath(s.Root, update); err != nil {
		return err
	}
	if _, err = os.Lstat(update); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("meta-update 必须不存在；保留现有内容，未请求原生更新")
	}
	backup := filepath.Join(dir, "meta-backup")
	if err = safePath(s.Root, backup); err != nil {
		return err
	}
	info, err := os.Lstat(backup)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.Mode().Perm()&0022 != 0 || !ok || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("meta-backup 必须是本用户拥有、不可由其他用户写入的普通目录")
	}
	entries, err := os.ReadDir(backup)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "mihomo" {
			return fmt.Errorf("meta-backup 含有未知内容；保留目录，未请求原生更新")
		}
		path := filepath.Join(backup, entry.Name())
		if err = safePath(s.Root, path); err != nil {
			return err
		}
		file, e := capture(path, maxProgram, false)
		if e != nil {
			return e
		}
		owner, valid := file.info.Sys().(*syscall.Stat_t)
		if !valid || owner.Uid != uint32(os.Geteuid()) || file.info.Mode().Perm()&0022 != 0 || file.info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return fmt.Errorf("原生核心备份的权限或归属无效")
		}
		if err = s.checkExecutable(file); err != nil {
			return err
		}
	}
	return nil
}

func controllerWait(ctx context.Context, deadline time.Time) error {
	if !time.Now().Before(deadline) {
		return fmt.Errorf("controller 操作期限内未确认新状态；保留原进程记录")
	}
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("controller 等待已取消；结果未确认：%w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

func (c *controllerSession) observe(ctx context.Context, directory directorySnapshot, upgrade bool, oldVersion string) error {
	timeout := c.s.StartTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var previous fileSnapshot
	previousVersion := ""
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("controller 等待已取消；结果未确认：%w", err)
		}
		if err := c.files(upgrade); err != nil {
			return err
		}
		if err := directory.check(); err != nil {
			return err
		}
		p, err := c.s.recordActual(c.record)
		if errors.Is(err, os.ErrNotExist) || err == nil && !sameBirth(p, c.process) {
			return fmt.Errorf("原受管出生实例已退出；未认领其他实例，结果未确认")
		}
		if err != nil && !errors.Is(err, errProcessTransition) {
			return fmt.Errorf("受管进程身份无法核验；保留原记录")
		}
		if err == nil && p.Executable == c.record.Program && !p.ExecutableDeleted {
			core := c.core
			if upgrade {
				core, err = c.s.programSnapshot()
				if err == nil {
					err = nativeCoreSafety(core)
				}
			}
			if err == nil && (!upgrade || hash(core.data) != c.record.ProgramHash) {
				image := true
				if upgrade {
					image, err = c.s.runner().(runningImageObserver).RunningImageMatches(p, core.path)
				}
				if err == nil && image {
					expected := c.record
					expected.Endpoints = nil
					for _, endpoint := range c.config.Endpoints {
						expected.Endpoints = append(expected.Endpoints, endpoint.URL())
					}
					ready, readyErr := c.s.recordReadyUntil(expected, p, deadline)
					if readyErr == nil && ready {
						version, versionErr := c.version(ctx, upgrade)
						if versionErr == nil && (!upgrade || version != oldVersion && stableTag.MatchString(version)) {
							if err := core.check(); err != nil {
								return err
							}
							if err := c.files(upgrade); err != nil {
								return err
							}
							if previous.info != nil && previousVersion == version && previous.check() == nil {
								if err := c.check(false, upgrade); err != nil {
									return err
								}
								ready, err := c.s.recordReadyUntil(expected, p, deadline)
								if err != nil || !ready || !time.Now().Before(deadline) {
									return fmt.Errorf("最终监听归属未在期限内确认；保留原记录")
								}
								if err := ctx.Err(); err != nil {
									return fmt.Errorf("controller 等待已取消；结果未确认：%w", err)
								}
								if upgrade {
									if err := c.s.nativeUpdatePaths(); err != nil {
										return fmt.Errorf("原生更新后目录状态不符合安全边界；结果未确认：%w", err)
									}
									matched, e := c.s.runner().(runningImageObserver).RunningImageMatches(p, core.path)
									if e != nil || !matched {
										return fmt.Errorf("最终运行映像无法确认；保留原记录")
									}
								}
								expected.ProgramHash, expected.ConfigHash, expected.Phase = hash(core.data), c.config.Digest, "running"
								expected.ControllerAddress, expected.ControllerDigest = c.control.Address, c.control.Digest()
								if err := core.check(); err != nil {
									return err
								}
								if err := c.files(upgrade); err != nil {
									return err
								}
								_, err = c.s.saveRecordExpected(expected, c.before, directory)
								return err
							}
							previous, previousVersion = core, version
						} else {
							previous = fileSnapshot{}
						}
					} else {
						previous = fileSnapshot{}
					}
				} else {
					previous = fileSnapshot{}
				}
			} else {
				previous = fileSnapshot{}
			}
		} else {
			previous = fileSnapshot{}
		}
		if err := controllerWait(ctx, deadline); err != nil {
			return err
		}
	}
}
