package proxy

import (
	"fmt"
	"net"
	"strconv"
)

// Endpoint is a configuration snapshot. It does not assert that a process is
// running or that its upstream can reach the network. Credentials are excluded.
type Endpoint struct {
	Name   string `json:"name"`
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
	// Authentication records configured authentication. Source-address bypass
	// rules may exempt a client, so consumers without credentials stay cautious.
	Authentication bool   `json:"authentication"`
	ConfigDigest   string `json:"config_digest"`
}

func (e Endpoint) URL() string {
	return e.Scheme + "://" + net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}

// Endpoint selects common, or the sole endpoint. It never chooses arbitrarily
// among multiple listeners and never embeds authentication in its result.
func (c Config) Endpoint(name string) (Endpoint, error) {
	if name != "" {
		for _, endpoint := range c.Endpoints {
			if endpoint.Name == name {
				return endpoint, nil
			}
		}
		return Endpoint{}, fmt.Errorf("未找到指定 Proxy 端点")
	}
	for _, endpoint := range c.Endpoints {
		if endpoint.Name == "common" {
			return endpoint, nil
		}
	}
	if len(c.Endpoints) == 1 {
		return c.Endpoints[0], nil
	}
	if len(c.Endpoints) == 0 {
		return Endpoint{}, fmt.Errorf("Proxy 没有可导出的端点")
	}
	return Endpoint{}, fmt.Errorf("Proxy 有多个端点；请显式指定名称")
}

func (s *Service) Endpoint(name string) (Endpoint, error) {
	config, err := s.ReadConfig()
	if err != nil {
		return Endpoint{}, err
	}
	return config.Endpoint(name)
}
