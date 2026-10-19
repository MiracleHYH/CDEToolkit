package distribution

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

const maxInstaller = 180 << 20
const maxInstallerCompressed = 64 << 20
const maxInstallerExecutable = 128 << 20

// Installer builds the self-contained POSIX bootstrap from the already
// verified release package. The repository retains only the small template;
// executable bytes exist only in generated release output.
func Installer(source, output string, verify bool) error {
	version, err := Version(filepath.Join(filepath.Dir(source), "..", "VERSION"))
	if err != nil {
		return err
	}
	binary, err := Verify(filepath.Join(output, "nido-linux-amd64.tar.gz"), version, "linux/amd64")
	if err != nil {
		return err
	}
	return installer(source, output, binary, version, verify)
}

// InstallerWithBinary checks the built executable and embeds it without
// invoking it; native release smoke tests independently check its version.
func InstallerWithBinary(source, binary, output, version string, verify bool) error {
	data, err := readFile(binary, maxBinary)
	if err != nil {
		return err
	}
	return installer(source, output, data, version, verify)
}

func installer(source, output string, binary []byte, version string, verify bool) error {
	if len(binary) > maxInstallerExecutable {
		return fmt.Errorf("安装器程序超出大小限制")
	}
	if err := checkBinary(binary, version, "linux/amd64"); err != nil {
		return err
	}
	data, err := readFile(source, 64<<10)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(data, []byte("#!/bin/sh\n")) || bytes.ContainsRune(data, '\r') {
		return fmt.Errorf("安装器必须是 LF 换行的 POSIX sh 模板")
	}
	tokens := [...]string{"@NIDO_VERSION@", "@NIDO_SHA256@", "@NIDO_SIZE@", "@NIDO_GZIP_SIZE@", "@NIDO_PAYLOAD@"}
	// Reject an invalid template before compressing the release program. The
	// potentially large payload is needed only for a complete, unique template.
	for _, token := range tokens {
		if bytes.Count(data, []byte(token)) != 1 {
			return fmt.Errorf("安装器模板标记缺失或重复: %s", token)
		}
	}
	var compressed bytes.Buffer
	writer, _ := gzip.NewWriterLevel(&compressed, gzip.BestCompression)
	if _, err = writer.Write(binary); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	if compressed.Len() > maxInstallerCompressed {
		return fmt.Errorf("安装器压缩载荷超出大小限制")
	}
	encoded := base64.StdEncoding.EncodeToString(compressed.Bytes())
	var payload strings.Builder
	for len(encoded) > 0 {
		n := min(76, len(encoded))
		payload.WriteString(encoded[:n])
		payload.WriteByte('\n')
		encoded = encoded[n:]
	}
	values := map[string]string{
		"@NIDO_VERSION@":   version,
		"@NIDO_SHA256@":    fmt.Sprintf("%x", sha256.Sum256(binary)),
		"@NIDO_SIZE@":      strconv.Itoa(len(binary)),
		"@NIDO_GZIP_SIZE@": strconv.Itoa(compressed.Len()),
		"@NIDO_PAYLOAD@":   strings.TrimSuffix(payload.String(), "\n"),
	}
	for _, token := range tokens {
		data = bytes.ReplaceAll(data, []byte(token), []byte(values[token]))
	}
	if len(data) > maxInstaller {
		return fmt.Errorf("安装器超出大小限制")
	}
	path := filepath.Join(output, "install.sh")
	if verify {
		actual, err := readFile(path, maxInstaller)
		if err != nil || !bytes.Equal(actual, data) {
			return fmt.Errorf("发布安装器与审核模板及程序不一致")
		}
		return nil
	}
	return writeFile(path, data)
}
