package cdet

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"debug/elf"
	"debug/macho"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
)

const maxDownload = 256 << 20
const maxArtifact = 512 << 20

// A single path for local/offline and official/custom HTTPS artifacts. No
// installer script or downloaded executable is run during file installation.
func (c *ReleaseClient) artifactBytes(a Artifact, name string) ([]byte, error) {
	if name == "codex" {
		return nil, fmt.Errorf("Codex 程序由其上游安装工具管理，不使用原生包解包器")
	}
	var data []byte
	var err error
	if strings.HasPrefix(a.Source, "https://") {
		if !validDigest(a.SHA256) {
			return nil, fmt.Errorf("HTTPS 安装必须提供有效 SHA-256")
		}
		data, err = c.get(a.Source, maxDownload)
	} else {
		if err = absolute(a.Source); err != nil {
			return nil, err
		}
		data, err = readRegularLarge(a.Source)
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxArtifact {
		return nil, fmt.Errorf("artifact 大小越界")
	}
	if a.SHA256 != "" && (!validDigest(a.SHA256) || !strings.EqualFold(a.SHA256, digest(data))) {
		return nil, fmt.Errorf("artifact SHA-256 不匹配")
	}
	var reader io.Reader = bytes.NewReader(data)
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		gz, e := gzip.NewReader(reader)
		if e != nil {
			return nil, e
		}
		defer gz.Close()
		reader = gz
	}
	// Stream archive headers, limiting the total expanded bytes as well as the
	// selected payload. Never extract filenames into the filesystem.
	limited := &io.LimitedReader{R: reader, N: maxArtifact + 1}
	buffered := bufio.NewReader(limited)
	header, _ := buffered.Peek(512)
	if len(header) >= 262 && string(header[257:262]) == "ustar" {
		tr := tar.NewReader(buffered)
		var binary []byte
		count := 0
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return nil, fmt.Errorf("无效 tar 归档: %w", e)
			}
			count++
			if count > 128 || h.Size < 0 || h.Size > maxArtifact {
				return nil, fmt.Errorf("归档成员数或大小越界")
			}
			clean := path.Clean(h.Name)
			if path.IsAbs(h.Name) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(h.Name, "\\") {
				return nil, fmt.Errorf("归档路径越界")
			}
			if h.Typeflag != tar.TypeReg {
				if h.Typeflag == tar.TypeDir {
					continue
				}
				return nil, fmt.Errorf("归档含链接或特殊文件")
			}
			base := path.Base(clean)
			wanted := moduleCommand(name)
			if base != wanted && !(name == "gds" && base == "scheduler") {
				continue
			}
			if binary != nil {
				return nil, fmt.Errorf("归档存在多个目标模块的可执行候选")
			}
			binary, e = io.ReadAll(tr)
			if e != nil {
				return nil, e
			}
		}
		// Consume the tail so gzip CRC and total expanded size are verified.
		if _, err = io.Copy(io.Discard, buffered); err != nil {
			return nil, err
		}
		if len(binary) == 0 {
			return nil, fmt.Errorf("归档未找到 %s 可执行文件", name)
		}
		data = binary
	} else {
		data, err = io.ReadAll(buffered)
		if err != nil {
			return nil, err
		}
	}
	if limited.N <= 0 || len(data) == 0 {
		return nil, fmt.Errorf("展开大小越界或程序为空")
	}
	script := bytes.HasPrefix(data, []byte("#!"))
	if script && name != "gds" {
		return nil, fmt.Errorf("%s 必须使用当前平台的独立 ELF/Mach-O 程序，不能安装依赖外部环境的脚本入口", name)
	}
	if !script && !bytes.HasPrefix(data, []byte("\x7fELF")) && !(len(data) >= 4 && (bytes.Equal(data[:4], []byte{0xcf, 0xfa, 0xed, 0xfe}) || bytes.Equal(data[:4], []byte{0xfe, 0xed, 0xfa, 0xcf}))) {
		return nil, fmt.Errorf("artifact 不是支持的 ELF/Mach-O 程序或带 shebang 的脚本")
	}
	if a.Repository != "" {
		if err = verifyPlatform(data, a.Platform); err != nil {
			return nil, err
		}
	} else if !script {
		if err = verifyPlatform(data, c.OS+"/"+c.Arch); err != nil {
			return nil, fmt.Errorf("本地或自定义程序不符合独立运行要求: %w", err)
		}
	}
	return data, nil
}

func verifyPlatform(data []byte, platform string) error {
	goos, arch, _ := strings.Cut(platform, "/")
	if goos == "linux" {
		f, err := elf.NewFile(bytes.NewReader(data))
		if err == nil {
			defer f.Close()
			want := elf.EM_X86_64
			if arch == "arm64" {
				want = elf.EM_AARCH64
			}
			if f.Machine == want && f.Class == elf.ELFCLASS64 && f.Data == elf.ELFDATA2LSB && (f.Type == elf.ET_EXEC || f.Type == elf.ET_DYN) {
				return verifyELFDependencies(f)
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
			if f.Cpu == want && f.Type == macho.TypeExec {
				return verifyMachODependencies(f)
			}
		}
	}
	return fmt.Errorf("官方包中的程序与 %s 不匹配", platform)
}

func systemDependencyPath(p, goos string) bool {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return false
	}
	roots := []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64"}
	if goos == "darwin" {
		roots = []string{"/usr/lib", "/System/Library"}
	}
	for _, root := range roots {
		if within(root, p) {
			return true
		}
	}
	return false
}

// The toolkit copies one program, never an old language/library environment.
// Reject native metadata that explicitly retains a non-system dependency.
func verifyELFDependencies(f *elf.File) error {
	for _, program := range f.Progs {
		if program.Type == elf.PT_DYNAMIC && f.SectionByType(elf.SHT_DYNAMIC) == nil {
			return fmt.Errorf("无法检查无节表 ELF 的动态依赖，请提供独立程序")
		}
		if program.Type != elf.PT_INTERP {
			continue
		}
		b, e := io.ReadAll(io.LimitReader(program.Open(), 4097))
		if e != nil || len(b) == 0 || len(b) > 4096 || b[len(b)-1] != 0 || !systemDependencyPath(string(b[:len(b)-1]), "linux") {
			return fmt.Errorf("ELF 加载器依赖非系统位置，请提供独立程序")
		}
	}
	for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
		values, e := f.DynString(tag)
		if e != nil {
			return fmt.Errorf("无法检查 ELF 库搜索路径: %w", e)
		}
		for _, value := range values {
			for _, p := range strings.Split(value, ":") {
				if !systemDependencyPath(p, "linux") {
					return fmt.Errorf("ELF 库搜索路径仍依赖原安装环境，请提供独立程序")
				}
			}
		}
	}
	libraries, e := f.ImportedLibraries()
	if e != nil {
		return fmt.Errorf("无法检查 ELF 动态库: %w", e)
	}
	for _, library := range libraries {
		if library == "" || strings.ContainsAny(library, "$\\") || strings.Contains(library, "/") && !systemDependencyPath(library, "linux") {
			return fmt.Errorf("ELF 动态库依赖原安装位置，请提供独立程序")
		}
	}
	return nil
}

func verifyMachODependencies(f *macho.File) error {
	for _, load := range f.Loads {
		raw := load.Raw()
		if len(raw) < 8 {
			return fmt.Errorf("Mach-O 加载命令无效")
		}
		command := f.ByteOrder.Uint32(raw)
		switch command {
		case 0xc, 0xe, 0x80000018, 0x8000001f, 0x20, 0x80000023, 0x8000001c:
			// dylib, dylinker, weak/re-export/lazy/upward dylib, rpath.
			if len(raw) < 12 {
				return fmt.Errorf("Mach-O 依赖命令无效")
			}
			offset := f.ByteOrder.Uint32(raw[8:])
			if offset < 12 || uint64(offset) >= uint64(len(raw)) {
				return fmt.Errorf("Mach-O 依赖路径无效")
			}
			name, _, terminated := bytes.Cut(raw[offset:], []byte{0})
			if !terminated || !systemDependencyPath(string(name), "darwin") {
				return fmt.Errorf("Mach-O 加载器、动态库或搜索路径仍依赖非系统位置，请提供独立程序")
			}
		}
	}
	return nil
}
