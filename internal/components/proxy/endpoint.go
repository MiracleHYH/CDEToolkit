package proxy

import (
	"fmt"
	"net"
	"strconv"

	"go.yaml.in/yaml/v3"
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

// Endpoint selects the sole endpoint when no name is supplied. Names are user
// data, not an API contract. It never chooses arbitrarily
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
	if len(c.Endpoints) == 1 {
		return c.Endpoints[0], nil
	}
	if len(c.Endpoints) == 0 {
		return Endpoint{}, fmt.Errorf("Proxy 没有可导出的端点")
	}
	return Endpoint{}, fmt.Errorf("Proxy 有多个端点；请显式指定名称")
}

// ListenerEndpoints exports only explicitly named listeners for application
// configuration. Traditional ports remain available to runtime ownership checks.
func (c Config) ListenerEndpoints() []Endpoint {
	var document yaml.Node
	if yaml.Unmarshal(c.Data, &document) != nil || len(document.Content) != 1 {
		return nil
	}
	listeners := field(document.Content[0], "listeners")
	if listeners == nil || listeners.Kind != yaml.SequenceNode {
		return []Endpoint{}
	}
	names := make(map[string]bool)
	for _, listener := range listeners.Content {
		name, err := stringValue(field(listener, "name"), "")
		if err == nil {
			names[name] = true
		}
	}
	result := []Endpoint{}
	for _, endpoint := range c.Endpoints {
		if names[endpoint.Name] {
			result = append(result, endpoint)
		}
	}
	return result
}

func (s *Service) ListenerEndpoints() ([]Endpoint, error) {
	config, err := s.ReadConfig()
	if err != nil {
		return nil, err
	}
	return config.ListenerEndpoints(), nil
}

func (s *Service) Endpoint(name string) (Endpoint, error) {
	config, err := s.ReadConfig()
	if err != nil {
		return Endpoint{}, err
	}
	return config.Endpoint(name)
}
