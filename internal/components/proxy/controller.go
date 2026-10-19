package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const maxControllerResponse = 4 << 20

// Overview is a bounded controller snapshot, not an upstream connectivity test.
type Overview struct {
	Version string        `json:"version"`
	Groups  []GroupStatus `json:"groups"`
}

type ProviderInfo struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	VehicleType string `json:"vehicle_type"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// A session binds one explicit request to a captured managed birth, files and
// controller authority. It is short lived and never persists credentials.
type controllerSession struct {
	s          *Service
	record     processRecord
	before     fileSnapshot
	core       fileSnapshot
	config     Config
	configFile fileSnapshot
	directory  directorySnapshot
	process    Process
	control    ControllerConfig
	client     *http.Client
	transport  *http.Transport
}

func (s *Service) controllerSession(allowConfigChanged, strict bool) (*controllerSession, error) {
	dir, program, err := s.paths()
	if err != nil {
		return nil, err
	}
	directory, err := captureDirectory(dir)
	if err != nil {
		return nil, err
	}
	r, before, err := s.readRecordSnapshot()
	if err != nil {
		return nil, fmt.Errorf("Proxy controller 需要已记录的受管运行实例")
	}
	if r.Phase != "running" {
		return nil, fmt.Errorf("Proxy controller 需要已就绪的受管运行实例")
	}
	config, err := s.ReadConfig()
	if err != nil {
		return nil, err
	}
	if config.Controller == nil {
		return nil, fmt.Errorf("当前 Proxy 配置没有启用 controller；请配置后显式 restart")
	}
	if !allowConfigChanged && config.Digest != r.ConfigHash {
		return nil, fmt.Errorf("Proxy 配置尚未应用；请先显式 reload 或 restart")
	}
	control := *config.Controller
	if r.ControllerDigest == "" {
		if config.Digest != r.ConfigHash {
			return nil, fmt.Errorf("旧进程记录未绑定 controller 且配置已更改；请显式 restart")
		}
	} else if r.ControllerAddress != control.Address || r.ControllerDigest != control.Digest() {
		return nil, fmt.Errorf("controller 地址或 secret 已更改；请恢复控制字段或显式 restart")
	}
	core, err := s.programSnapshot()
	if err != nil || hash(core.data) != r.ProgramHash {
		return nil, fmt.Errorf("Proxy 核心与运行记录不一致；未调用 controller")
	}
	configFile, err := capture(filepath.Join(dir, "config.yaml"), maxConfig, false)
	if err != nil || hash(configFile.data) != config.Digest {
		return nil, fmt.Errorf("Proxy 配置在 controller 检查期间发生变化")
	}
	p, err := s.recordActual(r)
	if err != nil || p.Executable != program || p.ExecutableDeleted {
		return nil, fmt.Errorf("无法确认受管 Proxy controller 的进程归属")
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		DisableKeepAlives:      true,
		ResponseHeaderTimeout:  2 * time.Minute,
		MaxResponseHeaderBytes: 32 << 10,
	}
	c := &controllerSession{s: s, record: r, before: before, core: core, config: config, configFile: configFile, directory: directory, process: p, control: control, transport: transport}
	c.client = &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if err = c.check(strict, false); err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	return c, nil
}

func (c *controllerSession) close() { c.transport.CloseIdleConnections() }

func (c *controllerSession) files(allowCoreChange bool) error {
	if err := c.directory.check(); err != nil {
		return err
	}
	if err := c.before.check(); err != nil {
		return fmt.Errorf("controller 操作期间进程记录变化；未重新认领实例")
	}
	if err := c.configFile.check(); err != nil {
		return fmt.Errorf("controller 操作期间主配置变化；结果未确认")
	}
	if !allowCoreChange {
		if err := c.core.check(); err != nil {
			return err
		}
	}
	return nil
}

func (c *controllerSession) check(strict, allowCoreChange bool) error {
	if err := c.files(allowCoreChange); err != nil {
		return err
	}
	p, err := c.s.recordActual(c.record)
	if err != nil || !sameBirth(p, c.process) || p.Executable != c.record.Program || p.ExecutableDeleted {
		return fmt.Errorf("controller 操作期间受管进程身份不可确认")
	}
	owned, err := c.s.runner().OwnsEndpoint(p, "http://"+c.control.Address)
	if err != nil || !owned {
		return fmt.Errorf("controller 监听不属于原受管出生实例")
	}
	if strict {
		ready, err := c.s.recordReady(c.record, p)
		if err != nil || !ready {
			return fmt.Errorf("Proxy 代理监听尚未全部就绪；未执行 controller 写操作")
		}
	}
	return c.files(allowCoreChange)
}

func controllerAllowed(method, path string) bool {
	switch method {
	case http.MethodGet:
		return path == "/version" || path == "/proxies" || path == "/providers/proxies" || path == "/providers/rules"
	case http.MethodPost:
		return path == "/upgrade?channel=release"
	case http.MethodPut:
		if path == "/configs?force=true" {
			return true
		}
		for _, prefix := range []string{"/providers/proxies/", "/providers/rules/"} {
			if strings.HasPrefix(path, prefix) {
				part := strings.TrimPrefix(path, prefix)
				decoded, err := url.PathUnescape(part)
				return err == nil && part == url.PathEscape(decoded) && providerNameOK(decoded)
			}
		}
	}
	return false
}

// request has no caller-supplied URL, proxy, headers or API extension point.
// Child error bodies may contain subscription/node credentials and are never
// copied into returned errors. A timeout/EOF does not authorize an auto retry.
func (c *controllerSession) request(ctx context.Context, method, path string, body []byte, status int, strict, changing bool) ([]byte, error) {
	if !controllerAllowed(method, path) {
		return nil, fmt.Errorf("不支持的 controller 请求")
	}
	timeout := 2 * time.Minute
	if method == http.MethodGet {
		timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := c.check(strict, changing && method == http.MethodGet); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.control.Address+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("无法构建固定 controller 请求")
	}
	req.Header.Set("Authorization", "Bearer "+c.control.Secret)
	if len(body) != 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("controller 请求已取消；结果未确认：%w", ctx.Err())
		}
		return nil, fmt.Errorf("controller 请求未完成；结果未确认，未自动重试")
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxControllerResponse+1))
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil || len(data) > maxControllerResponse {
		return nil, fmt.Errorf("controller 响应不完整或超过限制；结果未确认")
	}
	if resp.StatusCode != status {
		return nil, fmt.Errorf("controller 返回 HTTP %d；操作未确认", resp.StatusCode)
	}
	if status == http.StatusNoContent && len(bytes.TrimSpace(data)) != 0 {
		return nil, fmt.Errorf("controller 返回无效的空响应")
	}
	if changing {
		if err := c.files(true); err != nil {
			return nil, err
		}
	} else if err := c.check(strict, false); err != nil {
		return nil, err
	}
	return data, nil
}

func decodeController(data []byte, result any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(result); err != nil {
		return fmt.Errorf("controller JSON 响应无效")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("controller JSON 响应包含额外内容")
	}
	return nil
}

func (c *controllerSession) version(ctx context.Context, changing bool) (string, error) {
	data, err := c.request(ctx, http.MethodGet, "/version", nil, http.StatusOK, false, changing)
	if err != nil {
		return "", err
	}
	var version struct {
		Meta    bool   `json:"meta"`
		Version string `json:"version"`
	}
	if decodeController(data, &version) != nil || !version.Meta || len(version.Version) == 0 || len(version.Version) > 128 || strings.ContainsFunc(version.Version, unicode.IsControl) {
		return "", fmt.Errorf("controller 未返回有效 Mihomo 版本")
	}
	return version.Version, nil
}

func (s *Service) Overview() (Overview, error) { return s.OverviewContext(context.Background()) }

func (s *Service) OverviewContext(ctx context.Context) (Overview, error) {
	c, err := s.controllerSession(true, false)
	if err != nil {
		return Overview{}, err
	}
	defer c.close()
	version, err := c.version(ctx, false)
	if err != nil {
		return Overview{}, err
	}
	data, err := c.request(ctx, http.MethodGet, "/proxies", nil, http.StatusOK, false, false)
	if err != nil {
		return Overview{}, err
	}
	parsed, err := ParseProxiesResponse(data)
	if err != nil {
		return Overview{}, err
	}
	return Overview{Version: version, Groups: ResolveGroups(parsed)}, nil
}

func (s *Service) Groups() ([]GroupStatus, error) { return s.GroupsContext(context.Background()) }
func (s *Service) GroupsContext(ctx context.Context) ([]GroupStatus, error) {
	c, err := s.controllerSession(true, false)
	if err != nil {
		return nil, err
	}
	defer c.close()
	data, err := c.request(ctx, http.MethodGet, "/proxies", nil, http.StatusOK, false, false)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseProxiesResponse(data)
	if err != nil {
		return nil, err
	}
	return ResolveGroups(parsed), nil
}

func (s *Service) Providers() ([]ProviderInfo, error) {
	return s.ProvidersContext(context.Background())
}
func (s *Service) ProvidersContext(ctx context.Context) ([]ProviderInfo, error) {
	c, err := s.controllerSession(true, false)
	if err != nil {
		return nil, err
	}
	defer c.close()
	return c.providers(ctx)
}

func (c *controllerSession) providers(ctx context.Context) ([]ProviderInfo, error) {
	result := []ProviderInfo{}
	for _, pair := range []struct{ kind, path string }{{"proxy", "proxies"}, {"rule", "rules"}} {
		data, err := c.request(ctx, http.MethodGet, "/providers/"+pair.path, nil, http.StatusOK, false, false)
		if err != nil {
			return nil, err
		}
		var response struct {
			Providers map[string]struct {
				Type        string `json:"type"`
				VehicleType string `json:"vehicleType"`
				UpdatedAt   string `json:"updatedAt"`
			} `json:"providers"`
		}
		if decodeController(data, &response) != nil || response.Providers == nil {
			return nil, fmt.Errorf("controller provider 列表无效")
		}
		for name, provider := range response.Providers {
			if !providerNameOK(name) || len(provider.Type) > 128 || len(provider.VehicleType) > 128 || len(provider.UpdatedAt) > 128 || strings.ContainsFunc(provider.Type+provider.VehicleType+provider.UpdatedAt, unicode.IsControl) {
				return nil, fmt.Errorf("controller provider 字段无效")
			}
			result = append(result, ProviderInfo{Kind: pair.kind, Name: name, Type: provider.Type, VehicleType: provider.VehicleType, UpdatedAt: provider.UpdatedAt})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func providerNameOK(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 1024 && !strings.ContainsFunc(name, unicode.IsControl)
}

func (s *Service) RefreshProvider(kind, name string) error {
	return s.RefreshProviderContext(context.Background(), kind, name)
}
func (s *Service) RefreshProviderContext(ctx context.Context, kind, name string) error {
	if kind != "proxy" && kind != "rule" || !providerNameOK(name) {
		return fmt.Errorf("provider 需要指定 proxy/rule 类型及有效名称")
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
		providers, err := c.providers(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, provider := range providers {
			if provider.Kind == kind && provider.Name == name {
				vehicle := strings.ToLower(provider.VehicleType)
				if vehicle != "http" && vehicle != "file" {
					return fmt.Errorf("仅支持刷新 HTTP/file 原生 provider")
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("未找到指定 provider")
		}
		path := "proxies"
		if kind == "rule" {
			path = "rules"
		}
		_, err = c.request(ctx, http.MethodPut, "/providers/"+path+"/"+url.PathEscape(name), nil, http.StatusNoContent, true, false)
		return err
	})
}
