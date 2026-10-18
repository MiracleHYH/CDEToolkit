package distribution

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGDSTagIsIndependentAndExact(t *testing.T) {
	for _, tag := range []string{"gds-v0.1.0", "v0.1.0", "gds-v0.1.1", "gds-v0.1.0\n", "gds-v01.1.0", "gds-v0.1.0; exit 0"} {
		if (CheckGDSTag("0.1.0", tag) == nil) != (tag == "gds-v0.1.0") {
			t.Errorf("unexpected GDS tag result: %q", tag)
		}
		if CheckTag("0.1.0", tag) == nil {
			if tag != "v0.1.0" {
				t.Fatal("GDS tag accepted as a Nido tag")
			}
		}
	}
}

func TestGDSGlibcBaseline(t *testing.T) {
	for _, version := range []string{"GLIBC_2.2.5", "GLIBC_2.3", "GLIBC_2.31", "GLIBC_2.31.0"} {
		if !compatibleGlibc(version) {
			t.Error("rejected baseline symbol", version)
		}
	}
	for _, version := range []string{"", "GLIBC_PRIVATE", "GLIBC_2.31.1", "GLIBC_2.32", "GLIBC_3.0", "GLIBC_2.031", "GLIBC_2.-1", "GLIBC_2.2.5.0", "CXXABI_1.3", "GLIBC_2.31\n"} {
		if compatibleGlibc(version) {
			t.Error("accepted incompatible symbol", version)
		}
	}
}

func TestGDSLazyNVMLImportsDoNotWeakenGlibcGate(t *testing.T) {
	for _, item := range []struct {
		name, version string
		valid         bool
	}{
		{"nvmlGpuInstanceGetComputeInstanceProfileInfoV", "", true},
		{"pthread_create", "GLIBC_2.2.5", true},
		{"nvmlDeviceGetCount", "GLIBC_2.34", false},
		{"cuMemAlloc", "", false},
		{"unversioned_host_dependency", "", false},
		{"nvml/invalid", "", false},
		{"nvmlDeviceGetCount", "CUSTOM_ABI_1", false},
	} {
		if compatibleGDSImport(item.name, item.version) != item.valid {
			t.Errorf("unexpected import policy result: %+v", item)
		}
	}
}

func TestGDSPackRejectsForeignProgramBeforeWriting(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	out := filepath.Join(root, "out")
	if _, err = PackGDS(self, out, "0.1.0"); err == nil {
		t.Fatal("packaged a test program as GDS")
	}
	if _, err = os.Lstat(out); !os.IsNotExist(err) {
		t.Fatal("invalid GDS program created output")
	}
}

func TestGDSVerifyRequiresCanonicalAssetAndDigest(t *testing.T) {
	root := t.TempDir()
	data := []byte("not a GDS program")
	path := filepath.Join(root, GDSAsset)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, checksum := range []string{
		fmt.Sprintf("%064d  %s\n", 0, GDSAsset),
		fmt.Sprintf("%x  ../%s\n", sha256.Sum256(data), GDSAsset),
		fmt.Sprintf("%x  %s\n", sha256.Sum256(data), GDSAsset),
	} {
		if err := os.WriteFile(path+".sha256", []byte(checksum), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := VerifyGDS(path, "0.1.0"); err == nil {
			t.Fatal("accepted invalid checksum or non-GDS program")
		}
	}
	if _, err := VerifyGDS(filepath.Join(root, "gds"), "0.1.0"); err == nil {
		t.Fatal("accepted noncanonical release filename")
	}
}

func TestGDSSmokeRejectsForeignOrCrossPlatformProgram(t *testing.T) {
	if err := SmokeGDS([]byte("never execute this"), "0.1.0"); err == nil {
		t.Fatal("executed invalid GDS program on", runtime.GOOS, runtime.GOARCH)
	}
}
