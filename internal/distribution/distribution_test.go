package distribution

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVersionAndTagBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "VERSION")
	for _, value := range []string{"1.2.3\n", "0.0.0", "1.2.3\nextra", "v1.2.3", "01.2.3", "1.2.3\r\n", "1.2.3-dev", "1.2.3; echo unsafe"} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Version(path)
		valid := value == "1.2.3\n" || value == "0.0.0"
		if (err == nil) != valid {
			t.Errorf("unexpected version result for %q: %v", value, err)
		}
	}
	for _, tag := range []string{"v1.2.3", "1.2.3", "v1.2.4", "v1.2.3\n", `v1.2.3"; echo unsafe`} {
		if (CheckTag("1.2.3", tag) == nil) != (tag == "v1.2.3") {
			t.Error("accepted mismatched or unsafe release tag", tag)
		}
	}
}

func TestArchiveReproducibilityAndMinimalContents(t *testing.T) {
	var first, second bytes.Buffer
	for _, out := range []*bytes.Buffer{&first, &second} {
		if err := writeArchive(out, []byte("fixture-native-program")); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("identical executables produced different packages")
	}
	payload, err := unpack(first.Bytes())
	if err != nil || string(payload) != "fixture-native-program" {
		t.Fatal("package did not round-trip", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	header, err := tar.NewReader(gz).Next()
	if err != nil || header.ModTime.Unix() != 0 || header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" {
		t.Fatal("host time or identity leaked into package", err)
	}
}

func tarFixture(t *testing.T, headers []*tar.Header, tail []byte) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, header := range headers {
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg && header.Size < 1024 {
			if _, err := tw.Write(bytes.Repeat([]byte("x"), int(header.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = tw.Close() // The oversized-header case is intentionally incomplete.
	raw.Write(tail)
	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	if _, err := gz.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return packed.Bytes()
}

func TestArchiveRejectsExtraUnsafeAndCorruptContents(t *testing.T) {
	valid := func() *tar.Header {
		return &tar.Header{Name: "stk", Mode: 0755, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR, Size: 1}
	}
	for _, name := range []string{"traversal", "link", "directory", "mode", "extra-installer", "pax", "owner", "oversized", "truncated", "crc", "tail"} {
		t.Run(name, func(t *testing.T) {
			h := valid()
			headers := []*tar.Header{h}
			var tail []byte
			switch name {
			case "traversal":
				h.Name = "../stk"
			case "link":
				h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, "/outside", 0
			case "directory":
				h.Typeflag, h.Size = tar.TypeDir, 0
			case "mode":
				h.Mode = 0644
			case "extra-installer":
				headers = append(headers, &tar.Header{Name: "install.sh", Typeflag: tar.TypeReg, Mode: 0755, Size: 1, Format: tar.FormatUSTAR})
			case "pax":
				h.Format, h.PAXRecords = tar.FormatPAX, map[string]string{"comment": "private metadata"}
			case "owner":
				h.Uname = "private-host-user"
			case "oversized":
				h.Size = maxBinary + 1
			case "tail":
				tail = []byte("private trailing file")
			}
			data := tarFixture(t, headers, tail)
			if name == "truncated" {
				data = data[:len(data)-4]
			}
			if name == "crc" {
				data[len(data)-8] ^= 1
			}
			if _, err := unpack(data); err == nil {
				t.Fatal("unsafe or corrupted package accepted")
			}
		})
	}
}

func TestVerifyRequiresDigestFilenameAndGoIdentity(t *testing.T) {
	data := tarFixture(t, []*tar.Header{{Name: "stk", Typeflag: tar.TypeReg, Mode: 0755, Size: 1, Format: tar.FormatUSTAR}}, nil)
	root := t.TempDir()
	path := filepath.Join(root, "stk-linux-amd64.tar.gz")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, checksum := range []string{fmt.Sprintf("%064d  %s\n", 0, filepath.Base(path)), fmt.Sprintf("%x  /outside\n", sha256.Sum256(data)), fmt.Sprintf("%x  %s\n", sha256.Sum256(data), filepath.Base(path))} {
		if err := os.WriteFile(path+".sha256", []byte(checksum), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(path, "1.2.3", "linux/amd64"); err == nil {
			t.Fatal("wrong digest/path or a non-STK program was accepted")
		}
	}
}

func TestBuildOutputDoesNotFollowLinks(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "user-data")
	if err := os.WriteFile(outside, []byte("KEEP"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "asset")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(link, []byte("replace")); err == nil {
		t.Fatal("release output followed an existing link")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "KEEP" {
		t.Fatal("changed user content", err)
	}
}

func TestPackRejectsForeignProgramBeforeCreatingOutput(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "dist")
	if _, err = Pack(self, out, "1.2.3", runtime.GOOS+"/"+runtime.GOARCH); err == nil {
		t.Fatal("a test executable was packaged as the STK app")
	}
	if _, err = os.Lstat(out); !os.IsNotExist(err) {
		t.Fatal("invalid input created distribution files")
	}
}

func TestSmokeRejectsCrossPlatformBeforeExecution(t *testing.T) {
	platform := "linux/amd64"
	if runtime.GOOS+"/"+runtime.GOARCH == platform {
		platform = "darwin/arm64"
	}
	if err := Smoke([]byte("must never execute"), "1.2.3", platform); err == nil {
		t.Fatal("cross-platform program was executed")
	}
}
