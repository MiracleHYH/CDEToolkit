package conda

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

func sameMirrorNode(left, right *yaml.Node) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.Kind != right.Kind || left.Tag != right.Tag || left.Value != right.Value || len(left.Content) != len(right.Content) {
		return false
	}
	for i := range left.Content {
		if !sameMirrorNode(left.Content[i], right.Content[i]) {
			return false
		}
	}
	return true
}

func validatePresetMirrorContext(c Config, actual []byte) error {
	if c.PresetContext == "" && !c.Defaults.CondaCaptured {
		return nil
	}
	expectedYAML, err := PresetMirrorYAML(c)
	if err != nil {
		return err
	}
	expectedRoot, err := parseDefaultYAML(string(expectedYAML))
	if err != nil {
		return err
	}
	actualYAML, err := captureCondaYAMLForSnapshot(actual, string(expectedYAML))
	if err != nil {
		return fmt.Errorf("Conda 未返回完整的原有私有镜像映射配置")
	}
	actualRoot, err := parseDefaultYAML(actualYAML)
	if err != nil {
		return err
	}
	for _, name := range []string{"channel_alias", "migrated_channel_aliases", "migrated_custom_channels"} {
		if !sameMirrorNode(mirrorMapping(expectedRoot, name), mirrorMapping(actualRoot, name)) {
			return fmt.Errorf("Conda 实际 %s 与所保留的原有镜像上下文不一致", name)
		}
	}
	for _, name := range []string{"custom_channels", "custom_multichannels"} {
		expectedMap := mirrorMapping(expectedRoot, name)
		actualMap := mirrorMapping(actualRoot, name)
		for i := 0; i < len(expectedMap.Content); i += 2 {
			key := expectedMap.Content[i].Value
			if !sameMirrorNode(expectedMap.Content[i+1], mirrorMapping(actualMap, key)) {
				return fmt.Errorf("Conda 实际 %s 丢失或改变了所保留的频道映射", name)
			}
		}
		// Conda may add derived mappings for preset default URLs. Those are
		// harmless unless an unexpected mapping changes a selected named
		// channel which previously depended on channel_alias.
		for i := 0; i < len(actualMap.Content); i += 2 {
			key := actualMap.Content[i].Value
			if mirrorMapping(expectedMap, key) != nil {
				continue
			}
			for _, channel := range DesiredChannels(c) {
				if channel == key || name == "custom_channels" && strings.HasPrefix(channel, key+"/") {
					return fmt.Errorf("Conda 新增的 %s 改变了所选频道的原有解析方式", name)
				}
			}
		}
	}
	return nil
}
