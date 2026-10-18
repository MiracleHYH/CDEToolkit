package conda

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestPresetMirrorRetainsPrivateMappingsAliasAndMigrationRules(t *testing.T) {
	_, d := defaultsFixture(t)
	c := Config{CondaMirror: "tuna", Defaults: d}
	data, err := PresetMirrorYAML(c)
	if err != nil {
		t.Fatal(err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	original, err := parseDefaultYAML(d.CondaYAML)
	if err != nil {
		t.Fatal(err)
	}
	root := document.Content[0]
	for _, key := range []string{"channel_alias", "migrated_channel_aliases", "migrated_custom_channels"} {
		var before, after any
		_ = mirrorMapping(original, key).Decode(&before)
		_ = mirrorMapping(root, key).Decode(&after)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("preset changed original %s", key)
		}
	}
	before := mirrorMapping(mirrorMapping(original, "custom_channels"), "private")
	after := mirrorMapping(mirrorMapping(root, "custom_channels"), "private")
	if before == nil || after == nil || before.Value != after.Value {
		t.Fatal("preset lost the private named channel mapping")
	}
	for _, name := range []string{"team", "local"} {
		var before, after any
		_ = mirrorMapping(mirrorMapping(original, "custom_multichannels"), name).Decode(&before)
		_ = mirrorMapping(mirrorMapping(root, "custom_multichannels"), name).Decode(&after)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("preset changed private multichannel %s", name)
		}
	}
	defaults, custom := mirrorChannels("tuna")
	var actualDefaults []string
	if err := mirrorMapping(root, "default_channels").Decode(&actualDefaults); err != nil || !reflect.DeepEqual(actualDefaults, defaults) {
		t.Fatal("preset did not replace defaults")
	}
	for _, name := range []string{"conda-forge", "bioconda"} {
		if got := mirrorMapping(mirrorMapping(root, "custom_channels"), name); got == nil || got.Value != custom[name] {
			t.Fatalf("preset did not replace %s", name)
		}
	}
}

func TestPresetValidateDetectsLostPrivateContext(t *testing.T) {
	for _, changed := range []string{"", "private", "team", "channel_alias", "migrated_channel_aliases", "migrated_custom_channels", "new_selected_mapping", "unrelated_derived_mapping"} {
		t.Run(changed, func(t *testing.T) {
			_, d := defaultsFixture(t)
			c := Config{CondaMirror: "tuna", Defaults: d, PresetChannelsSet: true, PresetChannels: []string{"private", "team", "implicit"}}
			data, err := PresetMirrorYAML(c)
			if err != nil {
				t.Fatal(err)
			}
			var actual map[string]any
			if err := yaml.Unmarshal(data, &actual); err != nil {
				t.Fatal(err)
			}
			switch changed {
			case "private":
				delete(actual["custom_channels"].(map[string]any), "private")
			case "team":
				delete(actual["custom_multichannels"].(map[string]any), "team")
			case "channel_alias":
				actual[changed] = "https://conda.anaconda.org"
			case "migrated_channel_aliases":
				actual[changed] = []string{}
			case "migrated_custom_channels":
				actual[changed] = map[string]string{}
			case "new_selected_mapping":
				actual["custom_channels"].(map[string]any)["implicit"] = "https://new.example/base"
			case "unrelated_derived_mapping":
				actual["custom_channels"].(map[string]any)["pkgs/new"] = "https://derived.example/base"
			}
			encoded, err := json.Marshal(actual)
			if err != nil {
				t.Fatal(err)
			}
			err = validatePresetMirrorContext(c, encoded)
			if want := changed == "" || changed == "unrelated_derived_mapping"; (err == nil) != want {
				t.Fatalf("context accepted=%v expected=%v err=%v", err == nil, want, err)
			}
		})
	}
}

func TestPresetMirrorPrefersCurrentContextAndSelection(t *testing.T) {
	_, initial := defaultsFixture(t)
	current, err := parseDefaultYAML(initial.CondaYAML)
	if err != nil {
		t.Fatal(err)
	}
	setMirrorMapping(mirrorMapping(current, "custom_channels"), "later", yamlString("https://later-private.example/base"), false)
	sortMirrorMapping(mirrorMapping(current, "custom_channels"))
	context, err := encodeDefaultYAML(current)
	if err != nil {
		t.Fatal(err)
	}
	c := Config{CondaMirror: "official", Defaults: initial, PresetContext: context, PresetChannelsSet: true, PresetChannels: []string{"later", "team"}}
	data, err := PresetMirrorYAML(c)
	if err != nil {
		t.Fatal(err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	root := document.Content[0]
	if later := mirrorMapping(mirrorMapping(root, "custom_channels"), "later"); later == nil || later.Value != "https://later-private.example/base" {
		t.Fatal("preset lost a newly added private mapping")
	}
	var channels []string
	if err := mirrorMapping(root, "channels").Decode(&channels); err != nil || !reflect.DeepEqual(channels, c.PresetChannels) {
		t.Fatal("preset used stale initial channel selection")
	}
	if !bytes.Contains([]byte(initial.CondaYAML), []byte("untouched.private.test")) || bytes.Contains([]byte(initial.CondaYAML), []byte("later-private.example")) {
		t.Fatal("rendering changed the immutable initial default")
	}
}
