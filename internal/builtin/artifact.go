package builtin

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxProgram = 256 << 20

func (m *Manager) artifact(name, path, checksum string) ([]byte, error) {
	var raw []byte
	var err error
	if checksum != "" && !validChecksum(checksum) {
		return nil, fmt.Errorf("SHA-256 必须是 64 位十六进制摘要")
	}
	if path == "" || path == "latest" {
		if name != "proxy" {
			return nil, fmt.Errorf("%s 为可选内置组件；请显式提供本地独立可执行文件", name)
		}
		raw, err = m.downloadProxy(checksum)
	} else {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("本地程序必须使用绝对路径")
		}
		raw, err = readRegular(path, maxProgram)
		if err == nil && checksum != "" && !strings.EqualFold(checksum, hash(raw)) {
			err = fmt.Errorf("程序 SHA-256 不匹配")
		}
	}
	if err != nil {
		return nil, err
	}
	data := raw
	if bytes.HasPrefix(raw, []byte{0x1f, 0x8b}) {
		gz, e := gzip.NewReader(bytes.NewReader(raw))
		if e != nil {
			return nil, fmt.Errorf("无效 gzip 程序")
		}
		data, err = io.ReadAll(io.LimitReader(gz, maxProgram+1))
		closeErr := gz.Close()
		if err != nil || closeErr != nil || len(data) > maxProgram {
			return nil, fmt.Errorf("gzip 校验失败或展开大小越界")
		}
	}
	if err = nativeProgram(data, m.platform()); err != nil {
		return nil, err
	}
	return data, nil
}

func validChecksum(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == sha256.Size
}

func hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

var stableTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

func (m *Manager) downloadProxy(supplied string) ([]byte, error) {
	platform := strings.Split(m.platform(), "/")
	if len(platform) != 2 || platform[0] != "linux" && platform[0] != "darwin" || platform[1] != "amd64" && platform[1] != "arm64" {
		return nil, fmt.Errorf("在线 Proxy 安装仅支持 Linux/macOS 的 amd64/arm64")
	}
	api := m.API
	if api == "" {
		api = "https://api.github.com"
	}
	b, err := m.get(strings.TrimRight(api, "/")+"/repos/MetaCubeX/mihomo/releases/latest", 4<<20)
	if err != nil {
		return nil, err
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"assets"`
	}
	if json.Unmarshal(b, &release) != nil || !stableTag.MatchString(release.Tag) || release.Draft || release.Prerelease {
		return nil, fmt.Errorf("Mihomo 最新发布不是有效稳定版")
	}
	arches := []string{platform[1]}
	if platform[1] == "amd64" {
		// The unsuffixed amd64 package may require v3 instructions.
		arches = []string{"amd64-v1", "amd64-compatible"}
	}
	for _, arch := range arches {
		name := "mihomo-" + platform[0] + "-" + arch + "-" + release.Tag + ".gz"
		count := 0
		var source, digest string
		var size int64
		for _, a := range release.Assets {
			if a.Name == name {
				count++
				source, digest, size = a.URL, strings.TrimPrefix(a.Digest, "sha256:"), a.Size
				if !strings.HasPrefix(a.Digest, "sha256:") {
					return nil, fmt.Errorf("官方发布缺少 SHA-256 摘要")
				}
			}
		}
		if count == 0 {
			continue
		}
		if count != 1 || !validChecksum(digest) || size <= 0 || size > maxProgram {
			return nil, fmt.Errorf("官方发布包重复、摘要无效或大小越界")
		}
		expected := "https://github.com/MetaCubeX/mihomo/releases/download/" + release.Tag + "/" + name
		if source != expected || supplied != "" && !strings.EqualFold(supplied, digest) {
			return nil, fmt.Errorf("官方包地址或指定摘要不符合发布信息")
		}
		data, e := m.get(source, maxProgram)
		if e != nil {
			return nil, e
		}
		if int64(len(data)) != size || !strings.EqualFold(hash(data), digest) {
			return nil, fmt.Errorf("官方包大小或 SHA-256 校验失败")
		}
		return data, nil
	}
	return nil, fmt.Errorf("Mihomo 最新发布没有当前平台的兼容程序")
}

func (m *Manager) get(source string, max int64) ([]byte, error) {
	if err := httpsURL(source); err != nil {
		return nil, err
	}
	base := m.HTTP
	if base == nil {
		base = &http.Client{}
	}
	client := *base
	if client.Timeout == 0 || client.Timeout > 2*time.Minute {
		client.Timeout = 2 * time.Minute
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return fmt.Errorf("重定向过多")
		}
		return httpsURL(req.URL.String())
	}
	req, _ := http.NewRequest(http.MethodGet, source, nil)
	req.Header.Set("User-Agent", "Nido")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("官方发布下载失败；地址未输出")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength > max {
		return nil, fmt.Errorf("官方发布下载返回 HTTP %d 或大小越界", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil || int64(len(b)) > max {
		return nil, fmt.Errorf("官方发布下载不完整或大小越界")
	}
	return b, nil
}

func httpsURL(source string) error {
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("下载地址必须是无认证信息的 HTTPS URL")
	}
	return nil
}

func nativeProgram(data []byte, platform string) error {
	goos, arch, _ := strings.Cut(platform, "/")
	if goos == "linux" {
		f, err := elf.NewFile(bytes.NewReader(data))
		if err == nil {
			defer f.Close()
			want := elf.EM_X86_64
			if arch == "arm64" {
				want = elf.EM_AARCH64
			}
			if (arch == "amd64" || arch == "arm64") && f.Machine == want && f.Class == elf.ELFCLASS64 && f.Data == elf.ELFDATA2LSB && (f.Type == elf.ET_EXEC || f.Type == elf.ET_DYN) {
				for _, p := range f.Progs {
					if p.Type == elf.PT_DYNAMIC && f.SectionByType(elf.SHT_DYNAMIC) == nil {
						return fmt.Errorf("无法检查程序动态依赖；请提供独立程序")
					}
					if p.Type == elf.PT_INTERP {
						loader, e := io.ReadAll(io.LimitReader(p.Open(), 4097))
						if e != nil || len(loader) == 0 || len(loader) > 4096 || loader[len(loader)-1] != 0 || !systemLibrary(string(loader[:len(loader)-1]), goos) {
							return fmt.Errorf("程序依赖非系统加载器；请提供独立程序")
						}
					}
				}
				for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
					values, e := f.DynString(tag)
					if e != nil {
						return fmt.Errorf("无法检查程序动态依赖")
					}
					for _, value := range values {
						for _, p := range strings.Split(value, ":") {
							if !systemLibrary(p, goos) {
								return fmt.Errorf("程序依赖外部运行目录；请提供独立程序")
							}
						}
					}
				}
				libraries, e := f.ImportedLibraries()
				if e != nil {
					return fmt.Errorf("无法检查程序动态库")
				}
				for _, library := range libraries {
					if library == "" || strings.ContainsAny(library, "$\\") || strings.Contains(library, "/") && !systemLibrary(library, goos) {
						return fmt.Errorf("程序依赖外部动态库；请提供独立程序")
					}
				}
				return nil
			}
		}
	} else if goos == "darwin" {
		f, err := macho.NewFile(bytes.NewReader(data))
		if err == nil {
			defer f.Close()
			want := macho.CpuAmd64
			if arch == "arm64" {
				want = macho.CpuArm64
			}
			if (arch == "amd64" || arch == "arm64") && f.Cpu == want && f.Type == macho.TypeExec {
				for _, load := range f.Loads {
					raw := load.Raw()
					if len(raw) < 8 {
						return fmt.Errorf("无效 Mach-O 加载命令")
					}
					switch f.ByteOrder.Uint32(raw) {
					case 0xc, 0xe, 0x80000018, 0x8000001f, 0x20, 0x80000023, 0x8000001c:
						if len(raw) < 12 {
							return fmt.Errorf("无效 Mach-O 依赖")
						}
						offset := f.ByteOrder.Uint32(raw[8:])
						if offset < 12 || int64(offset) >= int64(len(raw)) {
							return fmt.Errorf("无效 Mach-O 依赖路径")
						}
						path, _, ok := bytes.Cut(raw[offset:], []byte{0})
						if !ok || !systemLibrary(string(path), goos) {
							return fmt.Errorf("程序依赖外部运行目录；请提供独立程序")
						}
					}
				}
				return nil
			}
		}
	}
	return fmt.Errorf("请提供符合 %s 的独立 ELF/Mach-O 可执行程序", platform)
}

func systemLibrary(path, goos string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	roots := []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64"}
	if goos == "darwin" {
		roots = []string{"/usr/lib", "/System/Library"}
	}
	for _, root := range roots {
		if strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}
