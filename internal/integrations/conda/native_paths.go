package conda

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const nativePathLimit = 1 << 20

type nativeDirectory struct {
	path string
	info os.FileInfo
}

type nativePathSnapshot struct {
	path    string
	content []byte
	info    os.FileInfo
	parents []nativeDirectory
}

type nativePathChange struct {
	before nativePathSnapshot
	after  []byte
}

// NativeScope marks fields explicitly selected by the current command. A
// repeated selection reapplies that field; all other native fields remain
// untouched. Without a scope, the API applies only changed configuration fields.
type NativeScope struct {
	CondaMirror, EnvsDir, PkgsDir, PipCacheDir bool
}

// CheckPaths previews changing nonempty paths and the selected native channels.
// Empty paths stop tracking; off/reset do not restore native settings.
func (s Service) CheckPaths(previous, next Config, scope ...NativeScope) error {
	_, err := s.prepareNativePaths(previous, next, scope...)
	return err
}

// NativePathFiles lists the files a read-only preview would actually change.
func (s Service) NativePathFiles(previous, next Config, scope ...NativeScope) ([]string, error) {
	changes, err := s.prepareNativePaths(previous, next, scope...)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, change.before.path)
	}
	return paths, nil
}

// ChangePaths updates native user configuration in place. Its undo is solely
// compensation for a failed Nido configuration save; user reset never calls it.
// Undo refuses to replace any concurrent edit of the files written here.
func (s Service) ChangePaths(previous, next Config, scope ...NativeScope) (func() error, error) {
	return s.ChangePathsChecked(previous, next, nil, scope...)
}

// ChangePathsChecked connects a prior read-only capture guard to freshly
// prepared native snapshots before any write. The snapshots then reject edits
// made after the guard without overwriting those concurrent changes.
func (s Service) ChangePathsChecked(previous, next Config, check func() error, scope ...NativeScope) (func() error, error) {
	changes, err := s.prepareNativePaths(previous, next, scope...)
	if err != nil {
		return nil, err
	}
	if check != nil {
		if err := check(); err != nil {
			return nil, err
		}
	}
	var applied []struct{ before, after nativePathSnapshot }
	undo := func() error {
		var errs []error
		for i := len(applied) - 1; i >= 0; i-- {
			entry := applied[i]
			if err := entry.after.unchanged(); err != nil {
				errs = append(errs, err)
				continue
			}
			if entry.before.info == nil {
				if err := os.Remove(entry.after.path); err != nil {
					errs = append(errs, err)
				}
			} else if _, err := entry.after.replace(entry.before.content); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
	for _, change := range changes {
		after, err := change.before.replace(change.after)
		if err != nil {
			return nil, errors.Join(err, undo())
		}
		applied = append(applied, struct{ before, after nativePathSnapshot }{change.before, after})
	}
	return undo, nil
}

func (s Service) prepareNativePaths(previous, next Config, scopes ...NativeScope) ([]nativePathChange, error) {
	if len(scopes) > 1 {
		return nil, fmt.Errorf("原生配置修改只能指定一个操作范围")
	}
	if len(scopes) == 1 {
		scope := scopes[0]
		for _, row := range []struct {
			explicit bool
			before   *string
			after    string
		}{
			{scope.CondaMirror, &previous.CondaMirror, next.CondaMirror},
			{scope.EnvsDir, &previous.EnvsDir, next.EnvsDir},
			{scope.PkgsDir, &previous.PkgsDir, next.PkgsDir},
			{scope.PipCacheDir, &previous.PipCacheDir, next.PipCacheDir},
		} {
			*row.before = row.after
			if row.explicit && row.after != "" {
				*row.before = ""
			}
		}
	}
	channels, manageChannels, err := nativeDesiredChannels(next)
	if err != nil {
		return nil, err
	}
	// An unrelated cache/envs update must not replay an already-selected
	// mirror over native channel edits made since the last selection.
	manageChannels = manageChannels && (previous.CondaMirror != next.CondaMirror || len(scopes) == 0 && previous.SourceCONDARC != next.SourceCONDARC)
	updates := map[string]string{}
	for _, row := range []struct{ key, previous, next string }{
		{"envs_dirs", previous.EnvsDir, next.EnvsDir},
		{"pkgs_dirs", previous.PkgsDir, next.PkgsDir},
	} {
		if row.next != "" && row.next != row.previous {
			updates[row.key] = row.next
		}
	}
	pipChanged := next.PipCacheDir != "" && next.PipCacheDir != previous.PipCacheDir
	if len(updates) == 0 && !pipChanged && !manageChannels {
		return nil, nil
	}
	for _, row := range []struct{ key, target string }{
		{"CONDA_ENVS_PATH", updates["envs_dirs"]},
		{"CONDA_ENVS_DIRS", updates["envs_dirs"]},
		{"CONDA_PKGS_DIRS", updates["pkgs_dirs"]},
		{"PIP_CACHE_DIR", func() string {
			if pipChanged {
				return next.PipCacheDir
			}
			return ""
		}()},
	} {
		if row.target == "" {
			continue
		}
		if current, _ := envValue(s.environment(), row.key); current != "" && current != row.target {
			return nil, fmt.Errorf("%s 会覆盖原生配置；请先 unset 或处理该环境变量后重试", row.key)
		}
	}
	for _, path := range []string{updates["envs_dirs"], updates["pkgs_dirs"], func() string {
		if pipChanged {
			return next.PipCacheDir
		}
		return ""
	}()} {
		if path == "" {
			continue
		}
		actual, err := s.NormalizePath(path)
		if err != nil {
			return nil, err
		}
		if actual != path {
			return nil, fmt.Errorf("目录物理位置已变化，请重新选择: %s", path)
		}
	}
	var changes []nativePathChange
	if len(updates) != 0 || manageChannels {
		paths, err := s.nativeCondaPaths(next)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			before, err := s.readNativePath(path)
			if err != nil {
				return nil, err
			}
			if path == next.SourceCONDARC && before.info == nil {
				return nil, fmt.Errorf("原始 CONDARC 不存在；未创建仅含镜像的新文件，请先修复原始配置")
			}
			var mirror []byte
			if manageChannels && (next.CondaMirror == "default" || next.SourceCONDARC != "" && path == next.SourceCONDARC) {
				_, mirror = s.MirrorFile(next)
				if len(mirror) == 0 {
					return nil, fmt.Errorf("无法生成原始 CONDARC 的镜像配置")
				}
			}
			after, err := changeNativeCondaMirror(before.content, updates, channels, manageChannels, mirror)
			if err != nil {
				return nil, fmt.Errorf("无法修改 %s: %w", path, err)
			}
			if len(after) > nativePathLimit {
				return nil, fmt.Errorf("修改后的原生配置不能超过 1 MiB: %s", path)
			}
			if manageChannels && before.info != nil && before.info.Mode().Perm()&0044 != 0 {
				protected := channels
				if len(mirror) != 0 {
					protected, err = nativeMirrorStrings(mirror)
					if err != nil {
						return nil, err
					}
				}
				if err := protectNativeChannelSecrets(before.content, protected); err != nil {
					return nil, fmt.Errorf("无法修改 %s: %w", path, err)
				}
			}
			if !bytes.Equal(before.content, after) {
				changes = append(changes, nativePathChange{before, after})
			}
		}
	}
	if pipChanged {
		path, err := s.nativePipPath()
		if err != nil {
			return nil, err
		}
		before, err := s.readNativePath(path)
		if err != nil {
			return nil, err
		}
		after, err := changePipCache(before.content, next.PipCacheDir)
		if err != nil {
			return nil, fmt.Errorf("无法修改 %s: %w", path, err)
		}
		if len(after) > nativePathLimit {
			return nil, fmt.Errorf("修改后的原生配置不能超过 1 MiB: %s", path)
		}
		if !bytes.Equal(before.content, after) {
			changes = append(changes, nativePathChange{before, after})
		}
	}
	return changes, nil
}

func (s Service) nativeHome() (string, error) {
	home := s.UserHome
	if home == "" {
		home, _ = envValue(s.environment(), "HOME")
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("无法定位用户配置目录")
	}
	return home, nil
}

func (s Service) nativeCondaPath(next Config) (string, error) {
	env := s.environment()
	path, _ := envValue(env, "CONDARC")
	if next.CondaMirror != "" || path != "" && s.Root != "" && contains(s.Root, filepath.Clean(path)) {
		// A selected mirror replaces custom CONDARC; the ordinary user file
		// remains in Conda's search chain, so native paths belong there.
		path = ""
	}
	if path == "" {
		home, err := s.nativeHome()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, ".condarc")
	}
	if !nativeCondaFilename(path) {
		return "", fmt.Errorf("原生 CONDARC 文件名必须为 .condarc、condarc 或以 .yaml/.yml 结尾；Conda 会忽略其他名称")
	}
	return path, nil
}

func nativeCondaFilename(path string) bool {
	name := filepath.Base(path)
	return name == ".condarc" || name == "condarc" || filepath.Ext(name) == ".yaml" || filepath.Ext(name) == ".yml"
}

func (s Service) nativeCondaPaths(next Config) ([]string, error) {
	primary, err := s.nativeCondaPath(next)
	if err != nil {
		return nil, err
	}
	paths := []string{primary}
	if next.SourceCONDARC != "" && next.SourceCONDARC != primary {
		if s.Root != "" && contains(s.Root, filepath.Clean(next.SourceCONDARC)) {
			return nil, fmt.Errorf("原始 CONDARC 指向 Nido 软件目录，无法安全修改原生配置")
		}
		if !nativeCondaFilename(next.SourceCONDARC) {
			return nil, fmt.Errorf("原始 CONDARC 文件名不被 Conda 读取，未修改；请先选择 .condarc、condarc 或 .yaml/.yml 文件")
		}
		paths = append(paths, next.SourceCONDARC)
	}
	env := s.environment()
	explicit, _ := envValue(env, "CONDARC")
	managedCurrent := explicit != "" && s.Root != "" && contains(s.Root, filepath.Clean(explicit))
	if next.CondaMirror == "" && !managedCurrent {
		return paths, nil
	}
	if managedCurrent {
		explicit = ""
		if originalSet, _ := envValue(env, "_NIDO_CONDA_CONDARC_ORIGINAL_SET"); originalSet == "x" {
			explicit, _ = envValue(env, "_NIDO_CONDA_CONDARC_ORIGINAL")
		}
	}
	if explicit != "" && !slicesContainsPath(paths, explicit) {
		// Both files must retain native directory edits: releasing the mirror
		// restores an original explicit CONDARC to the upstream search chain.
		// This also applies after off/reset but before source removes the old
		// overlay from the calling shell.
		if s.Root != "" && contains(s.Root, filepath.Clean(explicit)) {
			return nil, fmt.Errorf("原始 CONDARC 指向 Nido 软件目录，无法安全修改原生配置")
		}
		if !nativeCondaFilename(explicit) {
			return nil, fmt.Errorf("原始 CONDARC 文件名不被 Conda 读取，未修改；请先选择 .condarc、condarc 或 .yaml/.yml 文件")
		}
		paths = append(paths, explicit)
	}
	return paths, nil
}

func slicesContainsPath(paths []string, target string) bool {
	for _, path := range paths {
		if path == target {
			return true
		}
	}
	return false
}

func (s Service) nativePipPath() (string, error) {
	env := s.environment()
	if explicit, _ := envValue(env, "PIP_CONFIG_FILE"); explicit != "" {
		if explicit == os.DevNull {
			return "", fmt.Errorf("PIP_CONFIG_FILE 禁用了配置文件；请先取消该覆盖")
		}
		if _, err := os.Lstat(explicit); err == nil {
			return explicit, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	home, err := s.nativeHome()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		base, _ := envValue(env, "XDG_DATA_HOME")
		if base == "" {
			base = filepath.Join(home, "Library", "Application Support")
		}
		dir := filepath.Join(base, "pip")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return filepath.Join(dir, "pip.conf"), nil
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		return filepath.Join(home, ".config", "pip", "pip.conf"), nil
	}
	base, _ := envValue(env, "XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "pip", "pip.conf"), nil
}

func nativeDesiredChannels(c Config) ([]string, bool, error) {
	if c.CondaMirror == "" || !c.Defaults.CondaCaptured {
		return nil, false, nil
	}
	if _, err := parseDefaultYAML(c.Defaults.CondaYAML); err != nil {
		return nil, false, err
	}
	if c.CondaMirror != "default" && c.CondaMirror != "tuna" && c.CondaMirror != "official" {
		return nil, false, fmt.Errorf("Conda 镜像选择无效")
	}
	return DesiredChannels(c), true, nil
}

func protectNativeChannelSecrets(content []byte, channels []string) error {
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return err
	}
	// An identical scalar anywhere in the original file is already exposed to
	// its existing readers; only newly copied secret-bearing URLs are blocked.
	existing := map[string]bool{}
	var visit func(*yaml.Node)
	visit = func(node *yaml.Node) {
		if node.Kind == yaml.ScalarNode {
			existing[node.Value] = true
		}
		for _, child := range node.Content {
			visit(child)
		}
	}
	visit(&document)
	for _, channel := range channels {
		parsed, err := url.Parse(channel)
		if existing[channel] {
			continue
		}
		sensitive := strings.Contains(channel, "/t/") || strings.Contains(channel, "?")
		if err == nil {
			sensitive = sensitive || parsed.User != nil || strings.Contains(parsed.Path, "/t/")
		} else {
			sensitive = sensitive || strings.Contains(channel, "://") && strings.Contains(channel, "@")
		}
		if sensitive {
			return fmt.Errorf("频道地址含认证或令牌信息；请先将配置文件权限收紧为仅当前用户可读（例如 chmod 600），再重试")
		}
	}
	return nil
}

func changeCondaPaths(content []byte, updates map[string]string) ([]byte, error) {
	return changeNativeConda(content, updates, nil, false)
}

func nativeSequenceEqual(node *yaml.Node, values []string) bool {
	if node.Kind != yaml.SequenceNode || len(node.Content) != len(values) {
		return false
	}
	for i, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || item.Value != values[i] {
			return false
		}
	}
	return true
}

func changeNativeConda(content []byte, updates map[string]string, channels []string, manageChannels bool) ([]byte, error) {
	return changeNativeCondaMirror(content, updates, channels, manageChannels, nil)
}

type nativeCondaField struct {
	key, value *yaml.Node
}

// Only generated mirror keys are accepted here. The source configuration keeps
// its original place in Conda's search chain; unrelated nodes and their flags
// are retained in that same file instead of copied into an overlay.
func nativeMirrorFields(content []byte) (map[string]nativeCondaField, error) {
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&document); err != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("Conda 镜像配置必须是单一 YAML 映射")
	}
	var extra yaml.Node
	if decoder.Decode(&extra) != io.EOF || validateNativeYAML(&document) != nil {
		return nil, fmt.Errorf("Conda 镜像配置包含无效或歧义节点")
	}
	fields := make(map[string]nativeCondaField)
	root := document.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		key := root.Content[i]
		if !slicesContainsPath(defaultMirrorKeys, key.Value) {
			return nil, fmt.Errorf("Conda 镜像配置包含非镜像字段")
		}
		fields[key.Value] = nativeCondaField{key, root.Content[i+1]}
	}
	if len(fields) != legacyDefaultMirrorKeys && len(fields) != len(defaultMirrorKeys) {
		return nil, fmt.Errorf("Conda 镜像配置缺少必要字段")
	}
	for _, key := range defaultMirrorKeys[:len(fields)] {
		if _, ok := fields[key]; !ok {
			return nil, fmt.Errorf("Conda 镜像配置缺少必要字段")
		}
	}
	return fields, nil
}

func nativeMirrorStrings(content []byte) ([]string, error) {
	fields, err := nativeMirrorFields(content)
	if err != nil {
		return nil, err
	}
	var values []string
	var visit func(*yaml.Node)
	visit = func(node *yaml.Node) {
		if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
			values = append(values, node.Value)
		}
		for _, child := range node.Content {
			visit(child)
		}
	}
	for _, field := range fields {
		visit(field.value)
	}
	return values, nil
}

func nativeNodeEqual(a, b *yaml.Node) bool {
	if a.Kind != b.Kind || a.Tag != b.Tag || a.Value != b.Value || len(a.Content) != len(b.Content) {
		return false
	}
	// Mirror annotations control Conda's merge. Human comments and formatting
	// do not require a write, while any old merge annotation must be replaced.
	if nativeMergeComment(a.LineComment) != nativeMergeComment(b.LineComment) {
		return false
	}
	for i := range a.Content {
		if !nativeNodeEqual(a.Content[i], b.Content[i]) {
			return false
		}
	}
	return true
}

func nativeMergeComment(comment string) string {
	if strings.Contains(comment, "#!") {
		return strings.TrimSpace(comment)
	}
	return ""
}

func changeNativeCondaMirror(content []byte, updates map[string]string, channels []string, manageChannels bool, mirror []byte) ([]byte, error) {
	fields := map[string][]string{}
	for key, value := range updates {
		fields[key] = []string{value}
	}
	if manageChannels {
		fields["channels"] = channels
	}
	var mirrorFields map[string]nativeCondaField
	if len(mirror) != 0 {
		var err error
		mirrorFields, err = nativeMirrorFields(mirror)
		if err != nil {
			return nil, err
		}
	}
	keys := append([]string{"envs_dirs", "pkgs_dirs"}, defaultMirrorKeys...)
	nextField := func(key string) (nativeCondaField, bool) {
		if value, ok := mirrorFields[key]; ok {
			return value, true
		}
		if values, ok := fields[key]; ok {
			return nativeCondaField{yamlString(key), yamlStrings(values)}, true
		}
		return nativeCondaField{}, false
	}
	var document yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	err := decoder.Decode(&document)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("Conda 配置不是有效 YAML: %w", err)
	}
	if err == io.EOF {
		// A comment-only file is retained byte-for-byte before new keys.
		var b bytes.Buffer
		b.Write(content)
		if len(content) != 0 && content[len(content)-1] != '\n' {
			b.WriteByte('\n')
		}
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, key := range keys {
			if field, ok := nextField(key); ok {
				root.Content = append(root.Content, field.key, field.value)
			}
		}
		encoder := yaml.NewEncoder(&b)
		encoder.SetIndent(2)
		if err := encoder.Encode(root); err != nil {
			return nil, err
		}
		if err := encoder.Close(); err != nil {
			return nil, err
		}
		return b.Bytes(), nil
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("Conda 配置只能包含一个 YAML 文档")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("Conda 配置必须是 YAML 映射")
	}
	if err := validateNativeYAML(&document); err != nil {
		return nil, err
	}
	root := document.Content[0]
	changed := false
	for _, key := range keys {
		field, ok := nextField(key)
		if !ok {
			continue
		}
		next := field.value
		found := false
		for i := 0; i < len(root.Content); i += 2 {
			if root.Content[i].Value == key {
				old := root.Content[i+1]
				found = true
				_, isMirror := mirrorFields[key]
				equal := nativeNodeEqual(old, next)
				if isMirror {
					equal = equal && nativeMergeComment(root.Content[i].LineComment) == nativeMergeComment(field.key.LineComment)
				}
				if !equal {
					mirrorLineComment := next.LineComment
					if isMirror {
						field.key.HeadComment, field.key.FootComment = root.Content[i].HeadComment, root.Content[i].FootComment
						root.Content[i] = field.key
					}
					next.HeadComment, next.LineComment, next.FootComment = old.HeadComment, old.LineComment, old.FootComment
					if isMirror {
						// The generated flags replace old mirror merge controls;
						// comments on all non-mirror nodes are untouched.
						next.LineComment = mirrorLineComment
					}
					// yaml.v3 cannot emit a commented mapping key followed by
					// an empty collection. Preserve that comment on its value.
					if len(next.Content) == 0 && root.Content[i].LineComment != "" {
						if next.LineComment != "" {
							next.HeadComment = strings.TrimSpace(next.HeadComment + "\n" + next.LineComment)
						}
						next.LineComment, root.Content[i].LineComment = root.Content[i].LineComment, ""
					}
					root.Content[i+1] = next
					changed = true
				}
				break
			}
		}
		if !found {
			root.Content = append(root.Content, field.key, next)
			changed = true
		}
	}
	if !changed {
		return content, nil
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func validateNativeYAML(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode {
		return fmt.Errorf("含 YAML alias 的 Conda 配置需要先自行消除歧义")
	}
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
				return fmt.Errorf("Conda 配置包含重复或非字符串键")
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if err := validateNativeYAML(child); err != nil {
			return err
		}
	}
	return nil
}

func changePipCache(content []byte, path string) ([]byte, error) {
	if strings.TrimSpace(path) != path || strings.ContainsAny(path, "\r\n") {
		return nil, fmt.Errorf("pip 配置中的缓存目录不能包含首尾空白或换行")
	}
	text := string(content)
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.SplitAfter(text, "\n")
	section := ""
	sections := map[string]bool{}
	keys := map[string]bool{}
	globalEnd, targetStart, targetEnd := -1, -1, -1
	previousIndent := -1
	previousKey := ""
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		// RawConfigParser recognizes a more-indented continuation before
		// testing section syntax. A line such as "  [install]" can therefore
		// be data, and must never become an insertion boundary for this edit.
		prefix := raw[:len(raw)-len(strings.TrimLeftFunc(raw, unicode.IsSpace))]
		indent := utf8.RuneCountInString(prefix)
		if previousIndent >= 0 && indent > previousIndent {
			if previousKey == "global.cache-dir" {
				return nil, fmt.Errorf("pip 缓存目录包含多行值，请先自行整理")
			}
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.LastIndexByte(line, ']')
			tail := ""
			if end >= 0 {
				tail = strings.TrimSpace(line[end+1:])
			}
			if end <= 1 || tail != "" && !strings.HasPrefix(tail, "#") && !strings.HasPrefix(tail, ";") {
				return nil, fmt.Errorf("pip INI section 无法无歧义识别")
			}
			if section == "global" {
				globalEnd = i
			}
			// ConfigParser and pip preserve section case. [GLOBAL] does not
			// configure the effective [global] section.
			section = line[1:end]
			if sections[section] {
				return nil, fmt.Errorf("pip 配置包含重复 section")
			}
			sections[section] = true
			previousIndent, previousKey = -1, ""
			continue
		}
		separator := strings.IndexAny(line, "=:")
		if section == "" || separator <= 0 {
			return nil, fmt.Errorf("pip 配置包含无法无歧义识别的选项")
		}
		// Match pip's _normalize_name: option names are case-insensitive,
		// underscores are dashes, and a leading '--' is optional.
		key := strings.TrimPrefix(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(line[:separator])), "_", "-"), "--")
		identity := section + "." + key
		if keys[identity] {
			return nil, fmt.Errorf("pip 配置包含重复选项 %s", identity)
		}
		keys[identity] = true
		previousIndent, previousKey = indent, identity
		if identity == "global.cache-dir" || identity == "global.cache_dir" {
			if targetStart != -1 {
				return nil, fmt.Errorf("pip 缓存配置包含重复别名")
			}
			targetStart, targetEnd = i, i+1
		}
	}
	if section == "global" {
		globalEnd = len(lines)
	}
	replacement := "cache-dir = " + path + newline
	if targetStart >= 0 {
		original := lines[targetStart]
		replacement = original[:len(original)-len(strings.TrimLeftFunc(original, unicode.IsSpace))] + replacement
		return []byte(strings.Join(lines[:targetStart], "") + replacement + strings.Join(lines[targetEnd:], "")), nil
	}
	if sections["global"] {
		before := strings.Join(lines[:globalEnd], "")
		if before != "" && !strings.HasSuffix(before, "\n") {
			before += newline
		}
		return []byte(before + replacement + strings.Join(lines[globalEnd:], "")), nil
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += newline
	}
	return []byte(text + "[global]" + newline + replacement), nil
}

func nativeParents(path string) ([]nativeDirectory, error) {
	var result []nativeDirectory
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("配置父路径经过链接或非目录: %s", parent)
			}
			result = append(result, nativeDirectory{parent, info})
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		if parent == filepath.Dir(parent) {
			break
		}
	}
	return result, nil
}

func sameNativeInfo(a, b os.FileInfo) bool {
	if a == nil || b == nil || !os.SameFile(a, b) || a.Mode() != b.Mode() || a.Size() != b.Size() || !a.ModTime().Equal(b.ModTime()) {
		return false
	}
	left, lok := a.Sys().(*syscall.Stat_t)
	right, rok := b.Sys().(*syscall.Stat_t)
	return lok && rok && left.Nlink == 1 && right.Nlink == 1 && left.Uid == uint32(os.Geteuid()) && right.Uid == left.Uid && right.Gid == left.Gid
}

func (s Service) readNativePath(path string) (nativePathSnapshot, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.IndexFunc(path, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return nativePathSnapshot{}, fmt.Errorf("原生配置文件需要规范绝对路径")
	}
	if s.Root != "" && contains(s.Root, path) {
		return nativePathSnapshot{}, fmt.Errorf("原生用户配置不能位于 Nido 软件目录内")
	}
	return readNativePath(path)
}

func readNativePath(path string) (nativePathSnapshot, error) {
	parents, err := nativeParents(path)
	if err != nil {
		return nativePathSnapshot{}, err
	}
	out := nativePathSnapshot{path: path, parents: parents}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Size() > nativePathLimit || !ok || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) {
		return out, fmt.Errorf("配置必须是当前用户所有、不超过 1 MiB、无链接的普通文件: %s", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return out, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !sameNativeInfo(info, opened) {
		return out, fmt.Errorf("原生配置在读取期间发生变化")
	}
	content, err := io.ReadAll(io.LimitReader(f, nativePathLimit+1))
	finished, statErr := f.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || statErr != nil || pathErr != nil || len(content) > nativePathLimit || !sameNativeInfo(info, finished) || !sameNativeInfo(info, current) || int64(len(content)) != info.Size() {
		return out, fmt.Errorf("原生配置在读取期间发生变化")
	}
	out.info, out.content = info, content
	return out, out.checkParents()
}

func (s nativePathSnapshot) checkParents() error {
	if _, err := nativeParents(s.path); err != nil {
		return err
	}
	for _, parent := range s.parents {
		current, err := os.Lstat(parent.path)
		if err != nil || !current.IsDir() || !os.SameFile(parent.info, current) {
			return fmt.Errorf("原生配置父目录在操作期间发生变化")
		}
	}
	return nil
}

func (s nativePathSnapshot) unchanged() error {
	if err := s.checkParents(); err != nil {
		return err
	}
	current, err := readNativePath(s.path)
	if err != nil || !bytes.Equal(current.content, s.content) || (s.info != nil && !sameNativeInfo(s.info, current.info)) || (s.info == nil && current.info != nil) {
		return fmt.Errorf("原生配置已被其他操作修改，未覆盖: %s", s.path)
	}
	return nil
}

func (s nativePathSnapshot) replace(content []byte) (nativePathSnapshot, error) {
	return s.replaceWithRemove(content, os.Remove)
}

func (s nativePathSnapshot) replaceWithRemove(content []byte, remove func(string) error) (nativePathSnapshot, error) {
	if len(content) > nativePathLimit {
		return nativePathSnapshot{}, fmt.Errorf("原生配置不能超过 1 MiB")
	}
	if err := s.unchanged(); err != nil {
		return nativePathSnapshot{}, err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return nativePathSnapshot{}, err
	}
	parents, err := nativeParents(s.path)
	if err != nil {
		return nativePathSnapshot{}, err
	}
	s.parents = parents
	f, err := os.CreateTemp(filepath.Dir(s.path), ".nido-native-*")
	if err != nil {
		return nativePathSnapshot{}, err
	}
	name := f.Name()
	temporaryInfo, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nativePathSnapshot{}, err
	}
	cleanup := func() error {
		if err := s.checkParents(); err != nil {
			return err
		}
		current, err := os.Lstat(name)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil || !os.SameFile(temporaryInfo, current) {
			return fmt.Errorf("临时配置已被其他操作替换，未删除: %s", name)
		}
		return remove(name)
	}
	defer func() { _ = cleanup() }()
	mode := os.FileMode(0600)
	if s.info != nil {
		mode = s.info.Mode().Perm()
		// A temporary file inherits its parent directory's group. Preserve
		// the original group before copying potentially private configuration,
		// rather than changing the set of readers of a group-readable file.
		original, ok := s.info.Sys().(*syscall.Stat_t)
		temporary, statErr := f.Stat()
		if !ok || statErr != nil {
			err = fmt.Errorf("无法检查原生配置的组权限")
		} else if current, ok := temporary.Sys().(*syscall.Stat_t); !ok {
			err = fmt.Errorf("无法检查临时配置的组权限")
		} else if current.Gid != original.Gid {
			err = f.Chown(-1, int(original.Gid))
		}
	}
	if err == nil {
		err = f.Chmod(mode)
	}
	if err == nil {
		_, err = f.Write(content)
	}
	if syncErr := f.Sync(); err == nil {
		err = syncErr
	}
	created, statErr := f.Stat()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nativePathSnapshot{}, err
	}
	if statErr != nil {
		return nativePathSnapshot{}, statErr
	}
	if err := s.unchanged(); err != nil {
		return nativePathSnapshot{}, err
	}
	actual, err := os.Lstat(name)
	if err != nil || !sameNativeInfo(created, actual) {
		return nativePathSnapshot{}, fmt.Errorf("原生配置临时文件在操作期间发生变化")
	}
	if s.info == nil {
		err = os.Link(name, s.path)
		if err == nil {
			if cleanupErr := cleanup(); cleanupErr != nil {
				// Link has already published the destination. A failure while
				// removing its temporary hardlink must not hide that write from
				// compensation. Retry only our own temporary inode, then remove
				// the destination only if its complete receipt remains unchanged.
				retryErr := cleanup()
				published := nativePathSnapshot{path: s.path, content: content, info: created, parents: parents}
				undoErr := published.unchanged()
				if undoErr == nil {
					undoErr = remove(s.path)
				}
				if undoErr != nil {
					return nativePathSnapshot{}, fmt.Errorf("原生配置已创建但临时链接无法清理且本次新文件无法安全撤销，请检查 %s: %w", s.path, errors.Join(cleanupErr, retryErr, undoErr))
				}
				return nativePathSnapshot{}, fmt.Errorf("原生配置临时链接无法清理，本次新文件已撤销: %w", errors.Join(cleanupErr, retryErr))
			}
		}
	} else {
		err = os.Rename(name, s.path)
	}
	if err != nil {
		return nativePathSnapshot{}, fmt.Errorf("无法原子保存原生配置，未覆盖并发创建: %w", err)
	}
	return nativePathSnapshot{path: s.path, content: content, info: created, parents: parents}, nil
}
