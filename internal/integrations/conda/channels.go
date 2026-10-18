package conda

import (
	"fmt"
	"strings"
)

// DesiredChannels is the shared channel selection for the immutable overlay,
// native Conda file, and validation. Presets preserve the selection observed
// when that preset was chosen. The original default remains a separate snapshot.
func DesiredChannels(c Config) []string {
	if c.CondaMirror == "default" {
		root, err := parseDefaultYAML(c.Defaults.CondaYAML)
		if err != nil {
			return nil
		}
		for i := 0; i < len(root.Content); i += 2 {
			if root.Content[i].Value == "channels" {
				channels := make([]string, 0, len(root.Content[i+1].Content))
				for _, channel := range root.Content[i+1].Content {
					channels = append(channels, channel.Value)
				}
				return channels
			}
		}
		return nil
	}
	if c.CondaMirror != "official" && c.CondaMirror != "tuna" {
		return nil
	}
	channels := c.PresetChannels
	if !c.PresetChannelsSet && len(channels) == 0 {
		// Old installations did not save a per-selection channel list.
		context := c.Defaults
		if c.PresetContext != "" {
			context = MirrorDefaults{CondaCaptured: true, CondaYAML: c.PresetContext}
		}
		channels = PresetChannels(context, c.CondaMirror)
	}
	// Remapping an original URL and its named counterpart can identify the
	// same channel. Conda removes duplicates while preserving first priority.
	unique := make([]string, 0, len(channels))
	seen := make(map[string]bool, len(channels))
	for _, channel := range channels {
		if !seen[channel] {
			unique = append(unique, channel)
			seen[channel] = true
		}
	}
	return unique
}

func validatePresetChannels(c Config) error {
	if c.CondaMirror != "official" && c.CondaMirror != "tuna" {
		if c.PresetChannelsSet || len(c.PresetChannels) != 0 || c.PresetContext != "" {
			return fmt.Errorf("预设频道列表只能与 official 或 tuna 镜像一起保存")
		}
		return nil
	}
	if c.PresetContext != "" {
		if err := ValidateDefaults(MirrorDefaults{CondaCaptured: true, CondaYAML: c.PresetContext}); err != nil {
			return fmt.Errorf("预设镜像的原始频道上下文无效: %w", err)
		}
	}
	size := 0
	for _, channel := range c.PresetChannels {
		if channel == "" || channel != strings.TrimSpace(channel) || !snapshotText(channel) {
			return fmt.Errorf("预设频道列表包含无效频道名称或地址")
		}
		// Include room for YAML quoting/sequence punctuation without allowing
		// an oversized list to reach the generated environment resources.
		if len(channel) > maxOutput-size-16 {
			return fmt.Errorf("预设频道列表超过大小限制")
		}
		size += len(channel) + 16
	}
	return nil
}
