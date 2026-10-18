package distribution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
)

const GDSAsset = "gds-linux-amd64"

// go-nvml deliberately leaves its API calls unresolved until its explicit
// RTLD_LAZY|RTLD_GLOBAL load. These are not linked driver dependencies.
var delayedNVMLSymbol = regexp.MustCompile(`^nvml[A-Za-z0-9_]+$`)

func CheckGDSTag(version, tag string) error {
	if !stableVersion.MatchString(version) || tag != "gds-v"+version {
		return fmt.Errorf("GDS 发布标签必须与 GDS_VERSION 完全一致")
	}
	return nil
}

// CheckGDSBinary checks the native release identity and ABI without executing
// the program or loading NVIDIA libraries. The fixed Bullseye build baseline
// supports glibc 2.31 and later; neither CUDA nor NVML may be linked directly.
func CheckGDSBinary(data []byte, version string) error {
	if !stableVersion.MatchString(version) {
		return fmt.Errorf("无效 GDS 发布版本")
	}
	info, err := buildinfo.Read(bytes.NewReader(data))
	if err != nil || info.Path != "github.com/MiracleHYH/CDEToolkit/cmd/gds" || info.Main.Path != "github.com/MiracleHYH/CDEToolkit" {
		return fmt.Errorf("发布文件不是 GDS Go 主程序")
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != "linux" || settings["GOARCH"] != "amd64" || settings["CGO_ENABLED"] != "1" || settings["GOAMD64"] != "v1" {
		return fmt.Errorf("GDS 必须是 Linux amd64 v1 原生 cgo 构建")
	}
	f, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("GDS 发布文件不是 ELF: %w", err)
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 || f.Data != elf.ELFDATA2LSB || f.Machine != elf.EM_X86_64 || f.Type != elf.ET_EXEC {
		return fmt.Errorf("GDS ELF 架构或类型不符")
	}
	interpreter := false
	for _, prog := range f.Progs {
		if prog.Type != elf.PT_INTERP {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(prog.Open(), 256))
		if err != nil || string(data) != "/lib64/ld-linux-x86-64.so.2\x00" || interpreter {
			return fmt.Errorf("GDS ELF 动态加载器不符")
		}
		interpreter = true
	}
	if !interpreter {
		return fmt.Errorf("GDS ELF 缺少系统动态加载器")
	}
	libs, err := f.ImportedLibraries()
	if err != nil || len(libs) == 0 {
		return fmt.Errorf("无法确认 GDS glibc 依赖")
	}
	libc := false
	for _, lib := range libs {
		switch lib {
		case "libc.so.6":
			libc = true
		case "libpthread.so.0", "libdl.so.2":
		default:
			return fmt.Errorf("GDS 存在未允许的动态依赖: %s", lib)
		}
	}
	if !libc {
		return fmt.Errorf("GDS 缺少 glibc 依赖")
	}
	for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
		paths, err := f.DynString(tag)
		if err != nil || len(paths) != 0 {
			return fmt.Errorf("GDS 不允许嵌入动态库搜索路径")
		}
	}
	syms, err := f.ImportedSymbols()
	if err != nil || len(syms) == 0 {
		return fmt.Errorf("无法确认 GDS glibc 符号版本")
	}
	for _, sym := range syms {
		if !compatibleGDSImport(sym.Name, sym.Version) {
			return fmt.Errorf("GDS 符号 %s 需要未支持的 ABI %s", sym.Name, sym.Version)
		}
	}
	return nil
}

func compatibleGDSImport(name, version string) bool {
	return version == "" && delayedNVMLSymbol.MatchString(name) || compatibleGlibc(version)
}

func compatibleGlibc(version string) bool {
	if !strings.HasPrefix(version, "GLIBC_") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(version, "GLIBC_"), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	values := make([]int, len(parts))
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || strconv.Itoa(v) != p {
			return false
		}
		values[i] = v
	}
	return values[0] == 2 && (values[1] < 31 || values[1] == 31 && (len(values) == 2 || values[2] == 0))
}

// PackGDS copies exactly the raw native executable and its canonical digest.
// User data, configuration, installers and legacy scripts are never assets.
func PackGDS(binary, output, version string) (string, error) {
	data, err := readFile(binary, maxBinary)
	if err != nil {
		return "", err
	}
	if err = CheckGDSBinary(data, version); err != nil {
		return "", err
	}
	path := filepath.Join(output, GDSAsset)
	if err = writeFile(path, data); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	if err = writeFile(path+".sha256", []byte(hex.EncodeToString(sum[:])+"  "+GDSAsset+"\n")); err != nil {
		return "", err
	}
	return path, nil
}

func VerifyGDS(path, version string) ([]byte, error) {
	if filepath.Base(path) != GDSAsset {
		return nil, fmt.Errorf("GDS 发布文件名不符")
	}
	data, err := readFile(path, maxBinary)
	if err != nil {
		return nil, err
	}
	checksum, err := readFile(path+".sha256", 256)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if string(checksum) != hex.EncodeToString(sum[:])+"  "+GDSAsset+"\n" {
		return nil, fmt.Errorf("GDS 发布程序摘要或摘要文件名不符")
	}
	if err = CheckGDSBinary(data, version); err != nil {
		return nil, err
	}
	return data, nil
}

// SmokeGDS executes only --version --json in an empty environment. Native
// metadata reports build capabilities and never initializes CUDA or NVML.
func SmokeGDS(binary []byte, version string) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("GDS 只读冒烟需要 Linux amd64 原生执行环境")
	}
	if err := CheckGDSBinary(binary, version); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "gds-dist-smoke-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "gds")
	if err = os.WriteFile(path, binary, 0700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version", "--json")
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + filepath.Join(dir, "home"), "PATH=/usr/bin:/bin", "TMPDIR=" + dir}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("GDS 只读版本冒烟失败: %w: %s", err, out)
	}
	var metadata gdsapi.Metadata
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&metadata); err != nil || dec.Decode(new(any)) != io.EOF || metadata != (gdsapi.Metadata{Product: "gds", Version: version, Protocol: gdsapi.ProtocolVersion, Platform: "linux/amd64", Native: true}) {
		return fmt.Errorf("GDS 程序版本或协议元数据与发布目标不符")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "gds" {
		return fmt.Errorf("GDS 只读元数据命令创建了额外文件")
	}
	return nil
}
