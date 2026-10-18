package gdsapi

import (
	"bytes"
	"testing"
)

const testUUID = "GPU-12345678-1234-1234-1234-123456789abc"

func TestConfigStrictAndExplicit(t *testing.T) {
	c := DefaultConfig(testUUID)
	data, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseConfig(data)
	if err != nil || got != c {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	for name, input := range map[string][]byte{
		"unknown":    append(append([]byte{}, data...), []byte("\nunknown = 1\n")...),
		"duplicate":  append(append([]byte{}, data...), []byte("\nschema = 1\n")...),
		"missing":    bytes.ReplaceAll(data, []byte("max_idle_utilization_percent = 5\n"), nil),
		"ordinal":    bytes.ReplaceAll(data, []byte(testUUID), []byte("0")),
		"short uuid": bytes.ReplaceAll(data, []byte(testUUID), []byte("GPU-12345678")),
		"empty":      nil,
		"overflow":   bytes.ReplaceAll(data, []byte("idle_seconds = 1800"), []byte("idle_seconds = 999999999999999999999")),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseConfig(input); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	c.MaxIdleUtilizationPercent = 0
	data, err = c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseConfig(data); err != nil {
		t.Fatal("explicit zero utilization must be accepted", err)
	}
}

func TestConfigBounds(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.Schema = 2 }, func(c *Config) { c.IdleSeconds = 0 }, func(c *Config) { c.IdleSeconds = 604801 },
		func(c *Config) { c.ProbeIntervalSeconds = 0 }, func(c *Config) { c.ProbeIntervalSeconds = 11 },
		func(c *Config) { c.MemoryPercent = 0 }, func(c *Config) { c.MemoryPercent = 51 },
		func(c *Config) { c.MinFreeMiB = 0 }, func(c *Config) { c.MinFreeMiB = 1 << 21 },
		func(c *Config) { c.MaxIdleUtilizationPercent = -1 }, func(c *Config) { c.MaxIdleUtilizationPercent = 101 },
	} {
		c := DefaultConfig(testUUID)
		change(&c)
		if err := c.Validate(); err == nil {
			t.Fatalf("bad config accepted %+v", c)
		}
	}
}
