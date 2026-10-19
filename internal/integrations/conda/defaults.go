package conda

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// MirrorDefaults is the persistent, first-use mirror baseline. URL strings may
// contain credentials; JSON views expose only capture state, never addresses.
// Cache and environment directories are deliberately outside this snapshot.
type MirrorDefaults struct {
	CondaCaptured  bool              `toml:"conda_captured,omitempty" json:"conda_captured,omitempty"`
	CondaYAML      string            `toml:"conda_yaml,omitempty" json:"-"`
	CondaVariables map[string]string `toml:"conda_variables,omitempty" json:"-"`
	PipCaptured    bool              `toml:"pip_captured,omitempty" json:"pip_captured,omitempty"`
	PipIndexURL    string            `toml:"pip_index_url,omitempty" json:"-"`
	PipVariables   map[string]string `toml:"pip_variables,omitempty" json:"-"`
}

var condaMirrorVariables = []string{
	"CONDA_CHANNELS", "CONDA_CHANNEL", "CONDA_DEFAULT_CHANNELS", "CONDA_CUSTOM_CHANNELS",
	"CONDA_CUSTOM_MULTICHANNELS", "CONDA_CHANNEL_ALIAS", "CONDA_MIGRATED_CHANNEL_ALIASES",
	"CONDA_MIGRATED_CUSTOM_CHANNELS", "CONDA_RESTORE_FREE_CHANNEL",
}

// The first five keys form the original snapshot schema. Newly captured
// snapshots also retain migration rules because they determine how explicit
// old channel URLs resolve even when every one of the first five keys matches.
var defaultMirrorKeys = []string{"channels", "default_channels", "custom_channels", "custom_multichannels", "channel_alias", "migrated_channel_aliases", "migrated_custom_channels"}

const legacyDefaultMirrorKeys = 5

// MirrorVariableNames returns the finite Conda mirror override set owned by a
// selected profile. CONDARC and storage variables are managed separately.
func MirrorVariableNames() []string {
	return append([]string(nil), condaMirrorVariables...)
}

// PresetChannels preserves baseline channel selection and order, replacing only
// full URLs known to name the captured default or supported community channels.
// It never introduces a channel or rewrites an unrelated private channel.
func PresetChannels(d MirrorDefaults, mirror string) []string {
	if !d.CondaCaptured || mirror != "official" && mirror != "tuna" {
		return nil
	}
	root, err := parseDefaultYAML(d.CondaYAML)
	if err != nil {
		return nil
	}
	fields := map[string]*yaml.Node{}
	for i := 0; i < len(root.Content); i += 2 {
		fields[root.Content[i].Value] = root.Content[i+1]
	}
	targets, _ := mirrorChannels(mirror)
	type replacement struct{ source, target string }
	var replacements []replacement
	for _, channel := range fields["default_channels"].Content {
		path := channel.Value
		if parsed, err := url.Parse(channel.Value); err == nil && parsed.IsAbs() {
			path = parsed.EscapedPath()
		}
		for i, name := range []string{"main", "r", "msys2"} {
			if strings.HasSuffix(strings.TrimRight(path, "/"), "/"+name) {
				replacements = append(replacements, replacement{strings.TrimRight(channel.Value, "/"), targets[i]})
				break
			}
		}
	}
	custom := fields["custom_channels"]
	for _, name := range []string{"conda-forge", "bioconda"} {
		base := fields["channel_alias"].Value
		for i := 0; i < len(custom.Content); i += 2 {
			if custom.Content[i].Value == name {
				base = custom.Content[i+1].Value
				break
			}
		}
		source := strings.TrimRight(base, "/") + "/" + name
		if parsed, err := url.Parse(base); err == nil && parsed.IsAbs() {
			// An escaped separator in a private base is part of the original
			// endpoint. Appending through decoded Path would turn %2F into a
			// real slash and prevent matching that captured repository.
			path := strings.TrimRight(parsed.EscapedPath(), "/") + "/" + (&url.URL{Path: name}).EscapedPath()
			parsed.Path, _ = url.PathUnescape(path)
			parsed.RawPath = path
			source = parsed.String()
		}
		replacements = append(replacements, replacement{source, name})
	}
	channels := make([]string, 0, len(fields["channels"].Content))
	for _, channel := range fields["channels"].Content {
		value := channel.Value
		matched := false
		for _, mapping := range replacements {
			if target, ok := remapOriginalChannel(value, mapping.source, mapping.target); ok {
				value, matched = target, true
				break
			}
		}
		if !matched {
			if target, ok := remapStandardChannel(value, targets); ok {
				value = target
			}
		}
		channels = append(channels, value)
	}
	return channels
}

// Remapping an authenticated original repository to a public preset never
// forwards its userinfo, query parameters, fragments, or leading token path.
func remapOriginalChannel(value, source, target string) (string, bool) {
	actual, err := url.Parse(value)
	if err != nil {
		return "", false
	}
	original, err := url.Parse(source)
	if err != nil || !actual.IsAbs() || !original.IsAbs() || actual.Scheme != original.Scheme || !strings.EqualFold(actual.Host, original.Host) {
		return "", false
	}
	path, base := actual.EscapedPath(), strings.TrimRight(original.EscapedPath(), "/")
	if path != base && !strings.HasPrefix(path, base+"/") {
		return "", false
	}
	return target + withoutCondaTokens(strings.TrimPrefix(path, base)), true
}

func remapStandardChannel(value string, defaults []string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	// A path alone does not establish a public repository: organizations often
	// use /pkgs/main or /anaconda/cloud in their private servers too. Only
	// recognize these known public endpoints outside the explicit baseline map.
	if port := parsed.Port(); port != "" && (parsed.Scheme != "https" || port != "443") && (parsed.Scheme != "http" || port != "80") {
		return "", false
	}
	path := withoutCondaTokens(parsed.EscapedPath())
	var prefix string
	switch strings.ToLower(parsed.Hostname()) {
	case "repo.anaconda.com", "repo.continuum.io":
		prefix = ""
	case "mirrors.tuna.tsinghua.edu.cn", "mirrors.ustc.edu.cn", "mirrors.aliyun.com":
		prefix = "/anaconda"
	case "conda.anaconda.org":
		for _, name := range []string{"conda-forge", "bioconda"} {
			if suffix, ok := standardChannelSuffix(path, "/"+name); ok {
				return name + suffix, true
			}
		}
		return "", false
	default:
		return "", false
	}
	for i, name := range []string{"main", "r", "msys2"} {
		if suffix, ok := standardChannelSuffix(path, prefix+"/pkgs/"+name); ok {
			return defaults[i] + suffix, true
		}
	}
	if prefix != "" {
		for _, name := range []string{"conda-forge", "bioconda"} {
			if suffix, ok := standardChannelSuffix(path, prefix+"/cloud/"+name); ok {
				return name + suffix, true
			}
		}
	}
	return "", false
}

func standardChannelSuffix(path, marker string) (string, bool) {
	if !strings.HasPrefix(path, marker) {
		return "", false
	}
	suffix := strings.TrimPrefix(path, marker)
	if suffix != "" && !strings.HasPrefix(suffix, "/") {
		return "", false
	}
	return withoutCondaTokens(suffix), true
}

func withoutCondaTokens(path string) string {
	var parts []string
	for _, part := range strings.Split(path, "/") {
		// URL escapes can encode the token marker or even its separating slash.
		// Flatten encoded separators before recognizing /t/<token>, retaining
		// ordinary escaping in segments that do not contain a separator.
		decoded, err := url.PathUnescape(part)
		if err == nil && strings.Contains(decoded, "/") {
			for _, segment := range strings.Split(decoded, "/") {
				parts = append(parts, url.PathEscape(segment))
			}
		} else {
			parts = append(parts, part)
		}
	}
	kept := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		decoded, _ := url.PathUnescape(parts[i])
		if decoded == "t" && i+1 < len(parts) && parts[i+1] != "" {
			i++
			continue
		}
		kept = append(kept, parts[i])
	}
	return strings.Join(kept, "/")
}

func snapshotText(value string) bool {
	return utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func snapshotVariables(env []string, keys []string) map[string]string {
	values := map[string]string{}
	for _, key := range keys {
		if value, exists := envValue(env, key); exists {
			values[key] = value
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func validSnapshotVariables(values map[string]string, keys []string) bool {
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	for key, value := range values {
		if !allowed[key] || !snapshotText(value) {
			return false
		}
	}
	return true
}

// CaptureDefaults captures only missing tools and does not publish files. A
// failed all capture returns the original baseline so its caller can commit a
// complete result once. Repeated captures never replace an existing baseline.
func (s Service) CaptureDefaults(d MirrorDefaults, tool string) (MirrorDefaults, error) {
	if s.Ctx != nil && s.Ctx.Err() != nil {
		return d, s.Ctx.Err()
	}
	if tool != "conda" && tool != "pip" && tool != "all" {
		return d, fmt.Errorf("默认镜像捕获工具仅支持 conda、pip 或 all")
	}
	if err := ValidateDefaults(d); err != nil {
		return d, err
	}
	next := d
	if (tool == "conda" || tool == "all") && !d.CondaCaptured {
		data, err := s.run("conda", "config", "--show", "channels", "default_channels", "custom_channels", "custom_multichannels", "channel_alias", "migrated_channel_aliases", "migrated_custom_channels", "--json")
		if err != nil {
			if s.Ctx != nil && s.Ctx.Err() != nil {
				return d, s.Ctx.Err()
			}
			// An injected runner can return arbitrary diagnostics. The captured
			// source may contain secrets, so keep the public error value-free.
			return d, fmt.Errorf("无法读取 Conda 原始镜像配置；未保存 default 镜像")
		}
		content, err := captureCondaYAML(data)
		if err != nil {
			return d, err
		}
		next.CondaCaptured = true
		next.CondaYAML = content
		next.CondaVariables = snapshotVariables(s.environment(), condaMirrorVariables)
	}
	if (tool == "pip" || tool == "all") && !d.PipCaptured {
		data, err := s.run("python", "-m", "pip", "config", "list")
		if err != nil {
			if s.Ctx != nil && s.Ctx.Err() != nil {
				return d, s.Ctx.Err()
			}
			return d, fmt.Errorf("无法读取当前 Python 的 pip 原始镜像配置；未保存 default 镜像")
		}
		options, err := pipOptions(data)
		if err != nil {
			return d, fmt.Errorf("无法解析 pip 原始镜像配置；未保存 default 镜像")
		}
		// pip ignores empty configuration values. The separate environment
		// snapshot still records an explicitly present empty variable exactly.
		index := "https://pypi.org/simple"
		if value := options["global.index-url"]; value != "" {
			index = value
		}
		if value := options["install.index-url"]; value != "" {
			index = value
		}
		if value, _ := envValue(s.environment(), "PIP_INDEX_URL"); value != "" {
			index = value
		}
		if !validOriginalIndex(index) {
			return d, fmt.Errorf("pip 原始主索引地址无法保存；未保存 default 镜像")
		}
		next.PipCaptured = true
		next.PipIndexURL = index
		next.PipVariables = snapshotVariables(s.environment(), []string{"PIP_INDEX_URL"})
	}
	if err := ValidateDefaults(next); err != nil {
		return d, err
	}
	return next, nil
}

// ValidateDefaults validates only the captured mirror schema; it never checks
// current tool availability or whether a source file or data disk still exists.
func ValidateDefaults(d MirrorDefaults) error {
	if !d.CondaCaptured {
		if d.CondaYAML != "" || len(d.CondaVariables) != 0 {
			return fmt.Errorf("Conda default 镜像快照缺少捕获标记")
		}
	} else {
		root, err := parseDefaultYAML(d.CondaYAML)
		if err != nil {
			return err
		}
		canonical, err := encodeDefaultYAML(root)
		if err != nil || canonical != d.CondaYAML {
			return fmt.Errorf("Conda default 镜像快照不是规范受管 YAML")
		}
	}
	if !validSnapshotVariables(d.CondaVariables, condaMirrorVariables) {
		return fmt.Errorf("Conda default 镜像环境快照包含非镜像变量或无效内容")
	}
	if !d.PipCaptured {
		if d.PipIndexURL != "" || len(d.PipVariables) != 0 {
			return fmt.Errorf("pip default 镜像快照缺少捕获标记")
		}
	} else if !validOriginalIndex(d.PipIndexURL) {
		return fmt.Errorf("pip default 镜像主索引快照无效")
	}
	if !validSnapshotVariables(d.PipVariables, []string{"PIP_INDEX_URL"}) {
		return fmt.Errorf("pip default 镜像环境快照包含非镜像变量或无效内容")
	}
	if len(d.CondaYAML) > maxOutput || len(d.PipIndexURL) > maxOutput {
		return fmt.Errorf("default 镜像快照超过大小限制")
	}
	return nil
}

func validOriginalIndex(value string) bool {
	if value == "" || !snapshotText(value) || len(value) > maxOutput {
		return false
	}
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "http", "https":
		return u.Hostname() != ""
	case "file":
		return strings.HasPrefix(u.Path, "/")
	default:
		return false
	}
}

// DefaultMirrorFile computes the stable overlay for the saved original mirror.
// It does not rely on the old CONDARC path continuing to exist. Native non-mirror
// settings are not copied into the file and retain normal Conda merge semantics.
func (s Service) DefaultMirrorFile(d MirrorDefaults) (string, []byte) {
	if !d.CondaCaptured {
		return "", nil
	}
	content := []byte(d.CondaYAML)
	hash := sha256.Sum256(content)
	return filepath.Join(s.Root, "conda", fmt.Sprintf("mirror-%x.yaml", hash)), content
}

func yamlString(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func yamlStrings(values []string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		node.Content = append(node.Content, yamlString(value))
	}
	return node
}

func prepareDefaultComments(root *yaml.Node) {
	// yaml.v3 emits an empty collection on an unindented following line
	// when its key has a line comment. Put the same inline final annotation
	// on that empty value instead to keep valid YAML: channels: [] #!final.
	if root.Kind == yaml.MappingNode {
		for i := 0; i < len(root.Content); i += 2 {
			key, value := root.Content[i], root.Content[i+1]
			if (value.Kind == yaml.SequenceNode || value.Kind == yaml.MappingNode) && len(value.Content) == 0 && key.LineComment == "#!final" {
				key.LineComment = ""
				value.LineComment = "#!final"
			}
		}
	}
	for _, child := range root.Content {
		prepareDefaultComments(child)
	}
}

func encodeDefaultYAML(root *yaml.Node) (string, error) {
	return encodeMirrorYAML(root, "# Original Conda mirror configuration captured by Nido. May contain credentials.\n")
}

func encodeMirrorYAML(root *yaml.Node, header string) (string, error) {
	prepareDefaultComments(root)
	var data bytes.Buffer
	data.WriteString(header)
	encoder := yaml.NewEncoder(&data)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return data.String(), nil
}

func captureCondaYAML(data []byte) (string, error) {
	return captureCondaYAMLKeys(data, defaultMirrorKeys)
}

// Existing five-key snapshots are intentionally immutable. Their validation
// compares the fields they actually captured, while all new captures require
// the complete current schema including both migration rule fields.
func captureCondaYAMLForSnapshot(data []byte, snapshot string) (string, error) {
	root, err := parseDefaultYAML(snapshot)
	if err != nil {
		return "", err
	}
	return captureCondaYAMLKeys(data, defaultMirrorKeys[:len(root.Content)/2])
}

func captureCondaYAMLKeys(data []byte, keys []string) (string, error) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return "", fmt.Errorf("Conda 原始镜像配置不是有效 JSON")
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, key := range keys {
		value, exists := raw[key]
		if !exists || bytes.Equal(value, []byte("null")) {
			return "", fmt.Errorf("Conda 原始镜像配置缺少必要镜像字段")
		}
		var node *yaml.Node
		switch key {
		case "channels":
			var names []string
			if json.Unmarshal(value, &names) != nil {
				return "", fmt.Errorf("Conda 原始频道列表无效")
			}
			for _, name := range names {
				if name == "" || !snapshotText(name) {
					return "", fmt.Errorf("Conda 原始频道列表包含无效名称")
				}
			}
			node = yamlStrings(names)
		case "default_channels", "migrated_channel_aliases":
			values, err := originalChannels(value)
			if err != nil {
				return "", err
			}
			node = yamlStrings(values)
		case "channel_alias":
			address, err := originalChannel(value, true)
			if err != nil {
				return "", err
			}
			node = yamlString(address)
		case "custom_channels", "custom_multichannels", "migrated_custom_channels":
			var values map[string]json.RawMessage
			if json.Unmarshal(value, &values) != nil {
				return "", fmt.Errorf("Conda 原始自定义频道映射无效")
			}
			node = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			keys := make([]string, 0, len(values))
			for name := range values {
				keys = append(keys, name)
			}
			sort.Strings(keys)
			for _, name := range keys {
				if name == "" || !snapshotText(name) {
					return "", fmt.Errorf("Conda 原始自定义频道名称无效")
				}
				var entry *yaml.Node
				if key == "custom_multichannels" {
					channels, err := originalChannels(values[name])
					if err != nil {
						return "", err
					}
					entry = yamlStrings(channels)
				} else {
					address, err := originalChannel(values[name], true)
					if err != nil {
						return "", err
					}
					// Older string serializers include the channel name in
					// base_url; native custom_channels values omit that name.
					var text string
					if key == "custom_channels" && json.Unmarshal(values[name], &text) == nil {
						address = originalCustomBase(address, name)
					}
					entry = yamlString(address)
				}
				entryName := yamlString(name)
				if key == "custom_multichannels" {
					// A final flag on the parent mapping only limits later
					// sources; this entry-level flag replaces its whole list
					// instead of merging older per-source multichannel lists.
					entryName.LineComment = "#!final"
				}
				node.Content = append(node.Content, entryName, entry)
			}
		}
		keyNode := yamlString(key)
		keyNode.LineComment = "#!final"
		root.Content = append(root.Content, keyNode, node)
	}
	return encodeDefaultYAML(root)
}

// Strip the channel name from old full-URL serializers without decoding the
// base path. Both an encoded channel name and a name containing slashes may
// follow an encoded separator that belongs to the repository itself.
func originalCustomBase(address, name string) string {
	parsed, err := url.Parse(address)
	if err != nil || !parsed.IsAbs() {
		return strings.TrimSuffix(address, "/"+name)
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	decoded, _ := url.PathUnescape(path)
	if strings.HasSuffix(decoded, "/"+name) {
		// Locate the escaped prefix in one pass. Every valid %XX escape
		// contributes one decoded byte; literal UTF-8 bytes do so as well.
		prefixBytes := len(decoded) - len(name) - 1
		cut := 0
		for i := 0; i < prefixBytes; i++ {
			if path[cut] == '%' {
				cut += 3
			} else {
				cut++
			}
		}
		path, decoded = path[:cut], decoded[:prefixBytes]
	}
	parsed.Path = decoded
	parsed.RawPath = path
	return parsed.String()
}

func originalChannels(raw json.RawMessage) ([]string, error) {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		return nil, fmt.Errorf("Conda 原始频道序列无效")
	}
	channels := make([]string, 0, len(values))
	for _, value := range values {
		address, err := originalChannel(value, false)
		if err != nil {
			return nil, err
		}
		channels = append(channels, address)
	}
	return channels, nil
}

// originalChannel preserves credentials omitted by Channel.__str__/base_url.
// For custom mappings and channel_alias, base=true omits the separately named
// channel; a token belongs between location and that channel name in Conda URLs.
func originalChannel(raw json.RawMessage, base bool) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if text == "" || !snapshotText(text) {
			return "", fmt.Errorf("Conda 原始频道地址无效")
		}
		return text, nil
	}
	var channel struct {
		Scheme          string  `json:"scheme"`
		Location        string  `json:"location"`
		Name            *string `json:"name"`
		Auth            *string `json:"auth"`
		Token           *string `json:"token"`
		Platform        *string `json:"platform"`
		PackageFilename *string `json:"package_filename"`
	}
	if json.Unmarshal(raw, &channel) != nil || channel.Scheme == "" || channel.Location == "" && channel.Scheme != "file" {
		return "", fmt.Errorf("Conda 原始频道对象无效")
	}
	address := channel.Scheme + "://"
	if channel.Auth != nil && *channel.Auth != "" {
		address += *channel.Auth + "@"
	}
	// Channel locations can retain an original query. Append channel path
	// components before that query so token-bearing private sources survive.
	location, query, hasQuery := strings.Cut(channel.Location, "?")
	address += strings.TrimRight(location, "/")
	if channel.Token != nil && *channel.Token != "" {
		address += "/t/" + *channel.Token
	}
	if !base {
		for _, part := range []*string{channel.Name, channel.Platform, channel.PackageFilename} {
			if part != nil && *part != "" {
				address += "/" + strings.Trim(*part, "/")
			}
		}
	}
	if hasQuery {
		address += "?" + query
	}
	if !snapshotText(address) {
		return "", fmt.Errorf("Conda 原始频道对象包含无效内容")
	}
	return address, nil
}

func parseDefaultYAML(content string) (*yaml.Node, error) {
	if content == "" || len(content) > maxOutput {
		return nil, fmt.Errorf("Conda default 镜像快照为空或超限")
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(content))
	if decoder.Decode(&document) != nil || len(document.Content) != 1 {
		return nil, fmt.Errorf("Conda default 镜像快照不是有效 YAML")
	}
	var more yaml.Node
	if decoder.Decode(&more) != io.EOF {
		return nil, fmt.Errorf("Conda default 镜像快照必须是单个 YAML 文档")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode || len(root.Content) != legacyDefaultMirrorKeys*2 && len(root.Content) != len(defaultMirrorKeys)*2 {
		return nil, fmt.Errorf("Conda default 镜像快照字段无效")
	}
	for i, key := range defaultMirrorKeys[:len(root.Content)/2] {
		name, value := root.Content[2*i], root.Content[2*i+1]
		if name.Kind != yaml.ScalarNode || name.Tag != "!!str" || name.Value != key || name.LineComment != "#!final" && value.LineComment != "#!final" {
			return nil, fmt.Errorf("Conda default 镜像快照缺少规范镜像字段或 final 标记")
		}
		// yaml.v3 attaches inline comments to scalar and empty-container
		// values after parsing; our deterministic encoder starts with keys.
		name.LineComment = "#!final"
		value.LineComment = ""
		switch key {
		case "channels", "default_channels", "migrated_channel_aliases":
			if !stringSequence(value) {
				return nil, fmt.Errorf("Conda default 镜像快照频道列表无效")
			}
		case "channel_alias":
			if !stringScalar(value) {
				return nil, fmt.Errorf("Conda default 镜像快照频道别名无效")
			}
		case "custom_channels", "custom_multichannels", "migrated_custom_channels":
			if value.Kind != yaml.MappingNode || value.Tag != "!!map" || len(value.Content)%2 != 0 {
				return nil, fmt.Errorf("Conda default 镜像快照自定义频道映射无效")
			}
			previous := ""
			for j := 0; j < len(value.Content); j += 2 {
				name, entry := value.Content[j], value.Content[j+1]
				if !stringScalar(name) || name.Value <= previous || key != "custom_multichannels" && !stringScalar(entry) || key == "custom_multichannels" && !stringSequence(entry) {
					return nil, fmt.Errorf("Conda default 镜像快照自定义频道条目无效")
				}
				if key == "custom_multichannels" {
					if name.LineComment != "#!final" && entry.LineComment != "#!final" {
						return nil, fmt.Errorf("Conda default 多频道快照缺少条目 final 标记")
					}
					name.LineComment, entry.LineComment = "#!final", ""
				}
				previous = name.Value
			}
		}
	}
	// The generated header is emitted separately, not as a second YAML comment.
	root.HeadComment = ""
	root.Content[0].HeadComment = ""
	return root, nil
}

func stringScalar(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.Tag == "!!str" && node.Value != "" && snapshotText(node.Value) && node.Anchor == "" && node.Alias == nil
}

func stringSequence(node *yaml.Node) bool {
	if node.Kind != yaml.SequenceNode || node.Tag != "!!seq" || node.Anchor != "" || node.Alias != nil {
		return false
	}
	for _, child := range node.Content {
		if !stringScalar(child) {
			return false
		}
	}
	return true
}
