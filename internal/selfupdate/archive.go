package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"io"
)

func unpack(data []byte) ([]byte, error) {
	compressed := bytes.NewReader(data)
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, fmt.Errorf("Nido 发布包不是有效 gzip 文件")
	}
	defer gz.Close()
	// Releases contain one gzip stream. Do not accept appended archives or
	// trailing compressed bytes even when a tar reader would ignore them.
	gz.Multistream(false)
	limited := &io.LimitedReader{R: gz, N: maxBinary + 2049}
	tr := tar.NewReader(limited)
	h, err := tr.Next()
	if err != nil || h.Name != "nido" || h.Typeflag != tar.TypeReg || h.Format != tar.FormatUSTAR || h.Mode != 0755 || h.Size < 1 || h.Size > maxBinary || h.Linkname != "" || len(h.PAXRecords) != 0 || h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" {
		return nil, fmt.Errorf("Nido 发布包必须只包含普通可执行文件 nido")
	}
	binary, err := io.ReadAll(tr)
	if err != nil || int64(len(binary)) != h.Size {
		return nil, fmt.Errorf("Nido 发布包中的程序不完整")
	}
	if _, err := tr.Next(); err != io.EOF {
		return nil, fmt.Errorf("Nido 发布包含额外成员或无效 tar 数据")
	}
	// Consume through gzip EOF to validate CRC. A valid tar closes with its
	// own terminator; anything beyond that terminator is refused.
	// One byte proves a forbidden tail exists. Do not allocate or inflate the
	// entire remaining expansion budget just to reject an extra payload.
	tail, err := io.ReadAll(io.LimitReader(limited, 1))
	expectedSize := int64(512) + ((h.Size+511)/512)*512 + 1024
	if err != nil || limited.N <= 0 || len(tail) != 0 || compressed.Len() != 0 || maxBinary+2049-limited.N != expectedSize {
		return nil, fmt.Errorf("Nido 发布包尾部、压缩校验或展开大小无效")
	}
	return binary, nil
}

func checkBinary(data []byte, platform string) error {
	if err := checkProgramHeader(data, platform); err != nil {
		return err
	}
	info, err := buildinfo.Read(bytes.NewReader(data))
	if err != nil || info.Path != "github.com/MiracleHYH/CDEToolkit/cmd/nido" || info.Main.Path != "github.com/MiracleHYH/CDEToolkit" || info.Main.Replace != nil {
		return fmt.Errorf("Nido 发布文件不是预期的 Go 主程序")
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		if _, duplicate := settings[setting.Key]; duplicate {
			return fmt.Errorf("Nido 发布程序包含重复构建参数")
		}
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"]+"/"+settings["GOARCH"] != platform || settings["CGO_ENABLED"] != "0" {
		return fmt.Errorf("Nido 发布程序的平台或静态构建参数不符")
	}
	if platform == "linux/amd64" && settings["GOAMD64"] != "v1" {
		return fmt.Errorf("Nido 发布程序必须使用 GOAMD64=v1 基线")
	}
	return nil
}

func checkProgramHeader(data []byte, platform string) error {
	switch platform {
	case "linux/amd64", "linux/arm64":
		f, err := elf.NewFile(bytes.NewReader(data))
		if err == nil {
			defer f.Close()
			want := elf.EM_X86_64
			if platform == "linux/arm64" {
				want = elf.EM_AARCH64
			}
			if f.Machine == want && f.Class == elf.ELFCLASS64 && f.Data == elf.ELFDATA2LSB && (f.Type == elf.ET_EXEC || f.Type == elf.ET_DYN) {
				return nil
			}
		}
	case "darwin/amd64", "darwin/arm64":
		f, err := macho.NewFile(bytes.NewReader(data))
		if err == nil {
			defer f.Close()
			want := macho.CpuAmd64
			if platform == "darwin/arm64" {
				want = macho.CpuArm64
			}
			if f.Cpu == want && f.Magic == macho.Magic64 && f.Type == macho.TypeExec && f.ByteOrder == binary.LittleEndian {
				return nil
			}
		}
	}
	return fmt.Errorf("Nido 发布程序的实际文件格式或处理器架构不符")
}
