package gds

import (
	"bytes"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsutil"
	"io"
	"path/filepath"
	"strings"
)

const maxProgram = 256 << 20
const maxConfig = 64 << 10

type preparedArtifact struct {
	data            []byte
	expectedVersion string // Empty for an explicitly supplied local program.
	releaseTag      string
}

func validHash(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == sha256.Size }
func nativeProgram(data []byte, platform string) error {
	if platform != "linux/amd64" {
		return fmt.Errorf("GDS 首版仅支持 Linux amd64")
	}
	f, e := elf.NewFile(bytes.NewReader(data))
	if e != nil {
		return fmt.Errorf("GDS artifact 必须是 Linux amd64 ELF 程序")
	}
	defer f.Close()
	if f.Machine != elf.EM_X86_64 || f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || (f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN) {
		return fmt.Errorf("GDS artifact 架构或格式不匹配")
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			b, e := io.ReadAll(io.LimitReader(p.Open(), 4097))
			if e != nil || len(b) < 2 || len(b) > 4096 || b[len(b)-1] != 0 {
				return fmt.Errorf("无效 ELF loader")
			}
			loader := string(b[:len(b)-1])
			if strings.ContainsRune(loader, 0) || filepath.Clean(loader) != loader || (!strings.HasPrefix(loader, "/lib/") && !strings.HasPrefix(loader, "/lib64/")) {
				return fmt.Errorf("GDS 程序依赖非系统 loader")
			}
		}
	}
	for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
		v, e := f.DynString(tag)
		if e != nil {
			return e
		}
		if len(v) > 0 {
			return fmt.Errorf("GDS 程序不能依赖自定义动态库搜索路径")
		}
	}
	libs, e := f.ImportedLibraries()
	if e != nil {
		return e
	}
	for _, lib := range libs {
		if strings.ContainsAny(lib, "/$\\") {
			return fmt.Errorf("GDS 程序依赖非系统动态库路径")
		}
	}
	return nil
}
func (s *Service) artifact(path, checksum string) (preparedArtifact, error) {
	if err := ValidateSource(path, checksum); err != nil {
		return preparedArtifact{}, err
	}
	if officialSource(path) {
		if s.platform() != "linux/amd64" {
			return preparedArtifact{}, fmt.Errorf("GDS 首版仅支持 Linux amd64")
		}
		return s.downloadRelease(path, checksum)
	}
	b, e := gdsutil.Read(path, maxProgram)
	if e != nil {
		return preparedArtifact{}, e
	}
	if checksum != "" && !strings.EqualFold(checksum, gdsutil.Hash(b)) {
		return preparedArtifact{}, fmt.Errorf("GDS artifact SHA-256 不匹配")
	}
	return preparedArtifact{data: b}, nativeProgram(b, s.platform())
}
func (s *Service) metadata(program gdsutil.Snapshot) (gdsapi.Metadata, error) {
	var m gdsapi.Metadata
	b, e := s.runHelper(Command{Program: program.Path, ExpectedProgram: &program, Args: []string{"--version", "--json"}, Env: s.environment()})
	if e != nil {
		return m, e
	}
	if json.Unmarshal(b, &m) != nil || m.Product != "gds" || m.Protocol != gdsapi.ProtocolVersion || m.Version == "" || m.Platform != "linux/amd64" || !m.Native {
		return m, fmt.Errorf("GDS 程序身份、版本或协议不兼容")
	}
	return m, nil
}
