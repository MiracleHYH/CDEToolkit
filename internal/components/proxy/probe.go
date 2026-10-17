package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ProbeResult struct {
	TargetHost string `json:"target_host"`
	Endpoint   string `json:"endpoint"`
	StatusCode int    `json:"status_code"`
}

func CheckProbeURL(target string) error {
	u, err := url.Parse(target)
	if err != nil || strings.ContainsAny(target, "\x00\r\n") || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("探测目标必须是无凭据的 HTTP 或 HTTPS URL")
	}
	if strings.HasSuffix(u.Host, ":") {
		return fmt.Errorf("探测目标端口无效")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("探测目标端口无效")
		}
	}
	return nil
}

// CheckProbe performs only the static checks shared with a dry run.
func (s *Service) CheckProbe(name, target string) (Endpoint, error) {
	if err := CheckProbeURL(target); err != nil {
		return Endpoint{}, err
	}
	endpoint, err := s.Endpoint(name)
	if err != nil {
		return Endpoint{}, err
	}
	if endpoint.Authentication {
		return Endpoint{}, fmt.Errorf("该入口要求认证，未执行无凭据探测")
	}
	return endpoint, nil
}

// Probe is an explicit network operation through the selected, currently
// observed instance. It never changes configuration or starts the component.
func (s *Service) Probe(name, target string) (ProbeResult, error) {
	endpoint, err := s.CheckProbe(name, target)
	if err != nil {
		return ProbeResult{}, err
	}
	_, record, err := s.readRecordSnapshot()
	if err != nil {
		return ProbeResult{}, err
	}
	// Bind both observations to the same record capture. A replacement instance
	// with identical configuration must not turn this request into a successful
	// probe of a different process. This remains an OS observation, not a lease
	// on the listening socket between the check and the connection.
	checkInstance := func() error {
		if err := record.check(); err != nil {
			return fmt.Errorf("Proxy 探测期间进程记录发生变化")
		}
		runtime, err := s.Runtime()
		if err != nil {
			return err
		}
		if runtime.State != "running" || !runtime.Listeners || runtime.ConfigChanged || runtime.ConfigHash != endpoint.ConfigDigest {
			return fmt.Errorf("Proxy 入口尚未就绪或配置已变化")
		}
		if err := record.check(); err != nil {
			return fmt.Errorf("Proxy 探测期间进程记录发生变化")
		}
		return nil
	}
	if err := checkInstance(); err != nil {
		return ProbeResult{}, err
	}
	proxyURL, _ := url.Parse(endpoint.URL())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("无法构造探测请求")
	}
	response, err := client.Do(request)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("代理探测失败；目标及底层错误未输出")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return ProbeResult{}, fmt.Errorf("代理探测返回 HTTP %d", response.StatusCode)
	}
	if err := checkInstance(); err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{TargetHost: request.URL.Host, Endpoint: endpoint.Name, StatusCode: response.StatusCode}, nil
}
