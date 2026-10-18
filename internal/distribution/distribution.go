// Package distribution builds and checks release files. It is not linked into
// the installed CLI and never invokes an installer or a service controller.
package distribution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

const maxBinary = 512 << 20

var Platforms = []string{"linux/amd64"}
var stableVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func Version(path string) (string, error) {
	b, err := readFile(path, 128)
	if err != nil {
		return "", err
	}
	v := strings.TrimSuffix(string(b), "\n")
	if !stableVersion.MatchString(v) {
		return "", fmt.Errorf("VERSION 必须是单行稳定版本 X.Y.Z")
	}
	return v, nil
}

func CheckTag(version, tag string) error {
	if !stableVersion.MatchString(version) || tag != "v"+version {
		return fmt.Errorf("发布标签必须与 VERSION 完全一致")
	}
	return nil
}

func assetName(platform string) (string, error) {
	if !slices.Contains(Platforms, platform) {
		return "", fmt.Errorf("不支持的发布平台: %s", platform)
	}
	return "nido-" + strings.ReplaceAll(platform, "/", "-") + ".tar.gz", nil
}

func checkBinary(data []byte, version, platform string) error {
	if !stableVersion.MatchString(version) {
		return fmt.Errorf("无效发布版本")
	}
	if _, err := assetName(platform); err != nil {
		return err
	}
	info, err := buildinfo.Read(bytes.NewReader(data))
	if err != nil || info.Path != "github.com/MiracleHYH/CDEToolkit/cmd/nido" || info.Main.Path != "github.com/MiracleHYH/CDEToolkit" {
		return fmt.Errorf("发布文件不是 Nido Go 主程序")
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"]+"/"+settings["GOARCH"] != platform || settings["CGO_ENABLED"] != "0" {
		return fmt.Errorf("程序平台或静态构建参数与发布目标不符")
	}
	if settings["GOAMD64"] != "v1" {
		return fmt.Errorf("Linux amd64 发布程序必须使用 GOAMD64=v1 基线")
	}
	// Go omits linker flags from build info with -trimpath. Smoke verifies
	// the reported version on a native runner; all targets use the same flags.
	return nil
}

func Pack(binary, output, version, platform string) (string, error) {
	name, err := assetName(platform)
	if err != nil {
		return "", err
	}
	data, err := readFile(binary, maxBinary)
	if err != nil {
		return "", err
	}
	if err = checkBinary(data, version, platform); err != nil {
		return "", err
	}
	var archive bytes.Buffer
	if err = writeArchive(&archive, data); err != nil {
		return "", err
	}
	path := filepath.Join(output, name)
	if err = writeFile(path, archive.Bytes()); err != nil {
		return "", err
	}
	sum := sha256.Sum256(archive.Bytes())
	checksum := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	if err = writeFile(path+".sha256", []byte(checksum)); err != nil {
		return "", err
	}
	return path, nil
}

func writeArchive(out io.Writer, binary []byte) error {
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	header := &tar.Header{Name: "nido", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(binary)), ModTime: time.Unix(0, 0), Format: tar.FormatUSTAR}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	if _, err := tw.Write(binary); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func Verify(archive, version, platform string) ([]byte, error) {
	name, err := assetName(platform)
	if err != nil || filepath.Base(archive) != name {
		return nil, fmt.Errorf("发布包文件名与平台不符")
	}
	data, err := readFile(archive, maxBinary)
	if err != nil {
		return nil, err
	}
	checksum, err := readFile(archive+".sha256", 256)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if string(checksum) != hex.EncodeToString(sum[:])+"  "+name+"\n" {
		return nil, fmt.Errorf("发布包摘要或摘要文件名不符")
	}
	binary, err := unpack(data)
	if err == nil {
		err = checkBinary(binary, version, platform)
	}
	return binary, err
}

func unpack(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	limited := &io.LimitedReader{R: gz, N: maxBinary + 4097}
	tr := tar.NewReader(limited)
	header, err := tr.Next()
	if err != nil || header.Name != "nido" || header.Typeflag != tar.TypeReg || header.Format != tar.FormatUSTAR || header.Mode != 0755 || header.Size < 1 || header.Size > maxBinary || header.Linkname != "" || len(header.PAXRecords) != 0 || header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" {
		return nil, fmt.Errorf("发布包必须只含一个普通可执行文件 nido")
	}
	binary, err := io.ReadAll(tr)
	if err != nil {
		return nil, err
	}
	if _, err = tr.Next(); err != io.EOF {
		return nil, fmt.Errorf("发布包含额外成员或损坏的数据")
	}
	// Verify gzip CRC and bound even data after the tar terminator.
	tail, err := io.ReadAll(limited)
	if err != nil || limited.N <= 0 || len(bytes.Trim(tail, "\x00")) != 0 {
		return nil, fmt.Errorf("发布包尾部或展开大小无效")
	}
	return binary, nil
}

func readFile(path string, limit int64) ([]byte, error) {
	s, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !s.Mode().IsRegular() || s.Size() > limit {
		return nil, fmt.Errorf("构建输入必须是大小受限的普通文件: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("构建输入读取期间超出大小限制")
	}
	return data, err
}

func writeFile(path string, data []byte) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for p := abs; p != filepath.Dir(p); p = filepath.Dir(p) {
		s, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || s.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("构建输出不能经过符号链接: %s", p)
		}
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".nido-dist-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Chmod(0644)
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	return err
}
