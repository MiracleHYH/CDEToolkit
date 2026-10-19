package gdsapi

import (
	"bytes"
	"fmt"
	"regexp"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Schema                    int    `toml:"schema" json:"schema"`
	GPUUUID                   string `toml:"gpu_uuid" json:"gpu_uuid"`
	IdleSeconds               int    `toml:"idle_seconds" json:"idle_seconds"`
	ProbeIntervalSeconds      int    `toml:"probe_interval_seconds" json:"probe_interval_seconds"`
	MemoryPercent             int    `toml:"memory_percent" json:"memory_percent"`
	MinFreeMiB                int    `toml:"min_free_mib" json:"min_free_mib"`
	MaxIdleUtilizationPercent int    `toml:"max_idle_utilization_percent" json:"max_idle_utilization_percent"`
}

var uuidPattern = regexp.MustCompile(`^GPU-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func ValidUUID(uuid string) bool { return uuidPattern.MatchString(uuid) }

func DefaultConfig(uuid string) Config {
	return Config{Schema: 1, GPUUUID: uuid, IdleSeconds: 1800, ProbeIntervalSeconds: 5,
		MemoryPercent: 25, MinFreeMiB: 1024, MaxIdleUtilizationPercent: 5}
}

func (c Config) Validate() error {
	if c.Schema != 1 || !ValidUUID(c.GPUUUID) {
		return fmt.Errorf("GDS 配置 schema 或完整 GPU UUID 无效")
	}
	if c.IdleSeconds < 1 || c.IdleSeconds > 7*24*3600 {
		return fmt.Errorf("idle_seconds 必须为 1–604800")
	}
	if c.ProbeIntervalSeconds < 1 || c.ProbeIntervalSeconds > 10 {
		return fmt.Errorf("probe_interval_seconds 必须为 1–10")
	}
	if c.MemoryPercent < 1 || c.MemoryPercent > 50 {
		return fmt.Errorf("memory_percent 必须为 1–50")
	}
	if c.MinFreeMiB < 1 || c.MinFreeMiB > 1<<20 {
		return fmt.Errorf("min_free_mib 超出范围")
	}
	if c.MaxIdleUtilizationPercent < 0 || c.MaxIdleUtilizationPercent > 100 {
		return fmt.Errorf("max_idle_utilization_percent 必须为 0–100")
	}
	return nil
}

func ParseConfig(data []byte) (Config, error) {
	var c Config
	if len(data) == 0 || len(data) > 64<<10 {
		return c, fmt.Errorf("GDS 配置为空或大小越界")
	}
	// Required fields are checked separately: zero is a valid utilization limit.
	var keys map[string]any
	if err := toml.Unmarshal(data, &keys); err != nil {
		return c, fmt.Errorf("GDS TOML 无效: %w", err)
	}
	for _, key := range []string{"schema", "gpu_uuid", "idle_seconds", "probe_interval_seconds", "memory_percent", "min_free_mib", "max_idle_utilization_percent"} {
		if _, ok := keys[key]; !ok {
			return c, fmt.Errorf("GDS 配置缺少 %s", key)
		}
	}
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&c); err != nil {
		return c, fmt.Errorf("GDS 配置无效: %w", err)
	}
	return c, c.Validate()
}

func (c Config) Encode() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return toml.Marshal(c)
}

func Encode(c Config) ([]byte, error) { return c.Encode() }
