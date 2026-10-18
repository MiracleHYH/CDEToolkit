package conda

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"go.yaml.in/yaml/v3"
)

func TestDesiredChannelsPreservesCurrentSelectionAndDeduplicatesRemapping(t *testing.T) {
	s, original := defaultsFixture(t)
	channels := []string{"conda-forge", "https://later.private.test/team", "defaults", "conda-forge"}
	c := Config{CondaMirror: "tuna", Defaults: original, PresetChannels: channels, PresetChannelsSet: true, PresetContext: original.CondaYAML}
	if err := s.ValidateConfig(c); err != nil {
		t.Fatal(err)
	}
	want := []string{"conda-forge", "https://later.private.test/team", "defaults"}
	if got := DesiredChannels(c); !reflect.DeepEqual(got, want) {
		t.Fatalf("current channel selection was replaced or reordered: %v", got)
	}
	got := DesiredChannels(c)
	got[0] = "caller edit"
	if channels[0] != "conda-forge" {
		t.Fatal("DesiredChannels exposed the stored selection for mutation")
	}
	_, overlay := s.MirrorFile(c)
	var parsed struct {
		Channels []string `yaml:"channels"`
	}
	if err := yaml.Unmarshal(overlay, &parsed); err != nil || !reflect.DeepEqual(parsed.Channels, want) {
		t.Fatalf("overlay and native desired selection differ: %s; %v", overlay, err)
	}
	legacy := Config{CondaMirror: "tuna", Defaults: original}
	if !reflect.DeepEqual(DesiredChannels(legacy), PresetChannels(original, "tuna")) {
		t.Fatal("legacy configurations lost their original preset selection")
	}
	c.CondaMirror = "default"
	if reflect.DeepEqual(DesiredChannels(c), want) {
		t.Fatal("default incorrectly reused a later preset selection")
	}
}

func TestPresetChannelEmptySelectionAndContextSurviveTOMLWithoutPublicSecrets(t *testing.T) {
	s, original := defaultsFixture(t)
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "private-current-selection", true: "empty-current-selection"}[empty], func(t *testing.T) {
			c := Config{CondaMirror: "official", Defaults: original, PresetChannelsSet: true, PresetContext: original.CondaYAML}
			if !empty {
				c.PresetChannels = []string{"https://person:PRESET-PRIVATE-SECRET@private.test/team", "defaults"}
			}
			data, err := toml.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			var reloaded Config
			if err := toml.Unmarshal(data, &reloaded); err != nil || !reloaded.PresetChannelsSet || reloaded.PresetContext != c.PresetContext {
				t.Fatal("preset context or explicit selection state lost on reload", err)
			}
			if !reflect.DeepEqual(DesiredChannels(reloaded), DesiredChannels(c)) {
				t.Fatal("preset selection changed after reload")
			}
			if err := s.ValidateConfig(reloaded); err != nil {
				t.Fatal(err)
			}
			_, overlay := s.MirrorFile(reloaded)
			var parsed struct {
				Channels []string `yaml:"channels"`
			}
			if err := yaml.Unmarshal(overlay, &parsed); err != nil {
				t.Fatalf("invalid preset overlay: %s; %v", overlay, err)
			}
			if empty && len(parsed.Channels) != 0 {
				t.Fatalf("empty selection reverted to the original channels: %v", parsed.Channels)
			}
			public, err := json.Marshal(reloaded)
			if err != nil || bytes.Contains(public, []byte("SECRET")) || bytes.Contains(public, []byte("password")) || bytes.Contains(public, []byte("preset_context")) || bytes.Contains(public, []byte("preset_channels")) {
				t.Fatalf("public configuration leaked a private selection: %s; %v", public, err)
			}
		})
	}
}

func TestValidateConfigRejectsInvalidOrMisplacedPresetSelection(t *testing.T) {
	s, original := defaultsFixture(t)
	for _, cfg := range []Config{
		{PresetChannelsSet: true},
		{PresetChannels: []string{"private"}},
		{PresetContext: original.CondaYAML},
		{CondaMirror: "default", Defaults: original, PresetChannelsSet: true},
		{CondaMirror: "default", Defaults: original, PresetContext: original.CondaYAML},
		{CondaMirror: "tuna", PresetChannels: []string{""}},
		{CondaMirror: "tuna", PresetChannels: []string{"bad\nchannel"}},
		{CondaMirror: "tuna", PresetChannels: []string{" leading-space"}},
		{CondaMirror: "tuna", PresetChannels: []string{strings.Repeat("x", maxOutput)}},
		{CondaMirror: "tuna", PresetContext: original.CondaYAML + "unexpected_key: true\n"},
	} {
		if err := s.ValidateConfig(cfg); err == nil {
			t.Fatal("accepted an invalid or misplaced preset channel selection")
		}
	}
}
