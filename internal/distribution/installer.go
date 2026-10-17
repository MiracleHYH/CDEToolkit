package distribution

import (
	"bytes"
	"fmt"
	"path/filepath"
)

// Installer publishes the small POSIX downloader separately from the binary
// archives. It must be byte-for-byte the reviewed Go-CLI bootstrap source.
func Installer(source, output string, verify bool) error {
	data, err := readFile(source, 64<<10)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(data, []byte("#!/bin/sh\n")) || bytes.ContainsRune(data, '\r') {
		return fmt.Errorf("安装器必须是 LF 换行的 POSIX sh 脚本")
	}
	path := filepath.Join(output, "install.sh")
	if verify {
		actual, err := readFile(path, 64<<10)
		if err != nil || !bytes.Equal(actual, data) {
			return fmt.Errorf("发布安装器与审核源码不一致")
		}
		return nil
	}
	return writeFile(path, data)
}
