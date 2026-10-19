package proxy

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type coreRoundTripper func(*http.Request) (*http.Response, error)

func (f coreRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func coreReleaseFixture(t *testing.T, problem string) (*http.Client, string, *int) {
	t.Helper()
	count := new(int)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*count++
		if r.URL.Path != "/repos/MetaCubeX/mihomo/releases/latest" {
			t.Errorf("unexpected release request %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.2.4", "prerelease": problem == "prerelease"})
	}))
	t.Cleanup(server.Close)
	client := server.Client()
	base := client.Transport
	u, _ := url.Parse(server.URL)
	client.Transport = coreRoundTripper(func(r *http.Request) (*http.Response, error) {
		copy := r.Clone(r.Context())
		copy.URL.Host, copy.URL.Scheme = u.Host, u.Scheme
		copy.Host = u.Host
		return base.RoundTrip(copy)
	})
	return client, "https://api.github.com", count
}

func TestCoreLatestUsesIsolatedStableRelease(t *testing.T) {
	for _, problem := range []string{"", "prerelease"} {
		t.Run(problem, func(t *testing.T) {
			client, api, count := coreReleaseFixture(t, problem)
			s, runner, _ := coreService(t)
			s.HTTP, s.API = client, api
			version, err := s.Latest()
			if (err == nil) != (problem == "") || problem == "" && version != "v1.2.4" || *count != 1 {
				t.Fatalf("explicit metadata query: version=%s requests=%d err=%v", version, *count, err)
			}
			if len(runner.commands) != 0 || runner.starts != 0 || runner.stops != 0 {
				t.Fatal("metadata query executed or controlled core")
			}
		})
	}
}

func TestCoreAutoGitHubProxyIsRejectedBeforeNetworkOrExecution(t *testing.T) {
	client, api, _ := coreReleaseFixture(t, "")
	calls := 0
	client.Transport = coreRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("network must not be used")
	})
	s, runner, _ := coreService(t)
	s.Platform, s.HTTP, s.API, s.GitHubProxy = "linux/amd64", client, api, "auto"
	if err := s.Install("latest", ""); err == nil || calls != 0 || len(runner.commands) != 0 {
		t.Fatalf("removed Auto mode reached network/execution: calls=%d err=%v", calls, err)
	}
}

func TestCoreGitHubDownloadFailurePreservesInstalledProgramConfigAndUnknownData(t *testing.T) {
	s, runner, artifact := coreService(t)
	s.Platform = "linux/amd64"
	if err := os.WriteFile(artifact, coreNativeFixture("linux", "amd64"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
		t.Fatal(err)
	}
	dir, program, _ := s.paths()
	unknown := filepath.Join(dir, "unknown-cache")
	coreWrite(t, unknown, []byte("existing component data"))
	want := map[string][]byte{}
	for _, path := range []string{program, filepath.Join(dir, "config.yaml"), unknown} {
		want[path], _ = os.ReadFile(path)
	}
	commands := len(runner.commands)
	requests := 0
	s.GitHubProxy = "https://accelerator.invalid"
	s.HTTP = &http.Client{Transport: coreRoundTripper(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://api.github.com/user?access_token=secret"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})}
	if err := s.Update("latest", ""); err == nil || strings.Contains(err.Error(), "secret") || requests != 0 {
		t.Fatalf("unsafe update result: requests=%d err=%v", requests, err)
	}
	for path, before := range want {
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(after, before) {
			t.Fatalf("download failure changed existing data at %s: %v", path, err)
		}
	}
	if len(runner.commands) != commands || runner.starts != 0 || runner.stops != 0 {
		t.Fatal("failed download executed or controlled a component")
	}
}

func TestCoreGitHubRoutingDoesNotAffectLocalArtifactsOrDryRun(t *testing.T) {
	s, runner, artifact := coreService(t)
	s.GitHubProxy = "auto"
	s.HTTP = &http.Client{Transport: coreRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("local artifact or preview connected to a download service")
		return nil, nil
	})}
	for _, source := range []string{"latest", artifact} {
		if err := s.Preflight("install", source, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(runner.commands) != 0 {
		t.Fatal("preview executed a candidate")
	}
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Preflight("update", "latest", ""); err == nil {
		t.Fatal("native update preview accepted an unbound stopped core")
	}
	if err := s.Update(artifact, ""); err == nil {
		t.Fatal("legacy local update accepted")
	}
	if runner.starts != 0 || runner.stops != 0 {
		t.Fatal("local management controlled a process")
	}
}

func TestCoreVersionPreservesWholeReportedTokenForPinnedComparison(t *testing.T) {
	for _, version := range []string{"v1.19.32-", "v1.19.32+", "v1.19.32.", "v1.19.32/other"} {
		t.Run(version, func(t *testing.T) {
			s, r, artifact := coreService(t)
			r.run = func(c Command) ([]byte, error) { return []byte("Mihomo Meta " + version + " linux amd64"), nil }
			reported, err := s.version(artifact)
			if err == nil && reported == pinnedInstallVersion {
				t.Fatal("candidate version was truncated to the pinned stable tag")
			}
		})
	}
}

func TestCoreCheckUpdateDoesNotProposeDowngrade(t *testing.T) {
	for _, version := range []string{"v1.2.3", "v1.2.4", "v1.2.5", "v1.10.0", "v2.0.0", "v1.2.4-alpha-test"} {
		t.Run(version, func(t *testing.T) {
			client, api, _ := coreReleaseFixture(t, "")
			s, r, artifact := coreService(t)
			if err := s.Install(artifact, ""); err != nil {
				t.Fatal(err)
			}
			s.HTTP, s.API = client, api
			r.run = func(c Command) ([]byte, error) { return []byte("Mihomo Meta " + version), nil }
			info, err := s.CheckUpdate()
			if strings.Contains(version, "alpha") {
				if err == nil || info.Available {
					t.Fatalf("uncomparable version reported an update: %+v %v", info, err)
				}
				return
			}
			if err != nil || info.Available != (version == "v1.2.3") {
				t.Fatalf("unexpected update comparison: %+v %v", info, err)
			}
		})
	}
}

func TestCoreInstallDoesNotExecuteCandidateReplacedDuringVersionCheck(t *testing.T) {
	s, r, artifact := coreService(t)
	calls := 0
	r.run = func(c Command) ([]byte, error) {
		calls++
		if c.Args[0] != "-v" {
			t.Fatal("replaced candidate reached config validation")
		}
		if err := os.Rename(c.Program, c.Program+"-original"); err != nil {
			t.Fatal(err)
		}
		coreWrite(t, c.Program, []byte("unknown replacement"))
		return []byte("Mihomo Meta v1.2.4"), nil
	}
	if err := s.Install(artifact, ""); err == nil || calls != 1 {
		t.Fatalf("candidate replacement accepted: calls=%d error=%v", calls, err)
	}
}

func TestCoreLegacyLocalUpdateDoesNotDownloadExecuteOrReplace(t *testing.T) {
	s, r, artifact := coreService(t)
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
		t.Fatal(err)
	}
	_, program, _ := s.paths()
	before, err := capture(program, maxProgram, false)
	if err != nil {
		t.Fatal(err)
	}
	s.HTTP = &http.Client{Transport: coreRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("legacy update downloaded an artifact")
		return nil, nil
	})}
	commands := len(r.commands)
	for _, check := range []func() error{func() error { return s.Update(artifact, "") }, func() error { return s.Update("latest", strings.Repeat("0", 64)) }, func() error { return s.Preflight("update", artifact, "") }} {
		if err := check(); err == nil {
			t.Fatal("legacy local update was accepted")
		}
	}
	if err := before.check(); err != nil {
		t.Fatal(err)
	}
	if len(r.commands) != commands || r.starts != 0 || r.stops != 0 {
		t.Fatal("legacy update executed or controlled core")
	}
}

func TestCoreLocalArtifactRejectsWrongPlatformAndChecksums(t *testing.T) {
	for _, kind := range []string{"architecture", "checksum", "format", "gzip"} {
		t.Run(kind, func(t *testing.T) {
			s, r, artifact := coreService(t)
			checksum := ""
			switch kind {
			case "architecture":
				other := "amd64"
				if runtime.GOARCH == "amd64" {
					other = "arm64"
				}
				if err := os.WriteFile(artifact, coreNativeFixture(runtime.GOOS, other), 0700); err != nil {
					t.Fatal(err)
				}
			case "checksum":
				checksum = strings.Repeat("0", 64)
			case "format":
				if err := os.WriteFile(artifact, []byte("#!/bin/sh\n"), 0700); err != nil {
					t.Fatal(err)
				}
			case "gzip":
				if err := os.WriteFile(artifact, []byte{0x1f, 0x8b, 0, 0}, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Install(artifact, checksum); err == nil {
				t.Fatal("unsafe local artifact accepted")
			}
			if len(r.commands) != 0 {
				t.Fatal("invalid local artifact was executed")
			}
			_, program, _ := s.paths()
			if _, err := os.Stat(program); !os.IsNotExist(err) {
				t.Fatal("failed install left current program", err)
			}
		})
	}
}

func TestCoreCandidateVersionAndConfigRejectionPreserveConfiguration(t *testing.T) {
	for _, failure := range []string{"identity", "version-error", "config"} {
		t.Run(failure, func(t *testing.T) {
			s, r, artifact := coreService(t)
			dir, program, _ := s.paths()
			config := filepath.Join(dir, "config.yaml")
			coreWrite(t, config, []byte(coreConfig))
			before, err := capture(config, maxConfig, false)
			if err != nil {
				t.Fatal(err)
			}
			r.run = func(c Command) ([]byte, error) {
				if c.Args[0] == "-v" {
					if failure == "identity" {
						return []byte("arbitrary executable"), nil
					}
					if failure == "version-error" {
						return []byte("SECRET"), io.ErrUnexpectedEOF
					}
					return []byte("Mihomo Meta v1.2.4"), nil
				}
				return []byte("SECRET provider URL"), io.ErrUnexpectedEOF
			}
			err = s.Install(artifact, "")
			if err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("unsafe/absent candidate error: %v", err)
			}
			if err := before.check(); err != nil {
				t.Fatal("candidate rejection changed existing config", err)
			}
			if _, err := os.Lstat(program); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected install created core", err)
			}
			if matches, _ := filepath.Glob(filepath.Join(dir, ".nido-core-*")); len(matches) != 0 {
				t.Fatal("failed install leaked temporary core", matches)
			}
		})
	}
}

func coreDynamicELFFixture(arch string, tag elf.DynTag, value, loader string) []byte {
	const base = 0x400000
	programs := 2
	if loader != "" {
		programs++
	}
	dynamicOffset := 64 + programs*56
	stringsOffset := dynamicOffset + 64
	stringsData := append(append([]byte{0}, []byte(value)...), 0)
	loaderOffset := stringsOffset + len(stringsData)
	loaderData := []byte(loader)
	if loader != "" {
		loaderData = append(loaderData, 0)
	}
	sectionsOffset := (loaderOffset + len(loaderData) + 7) &^ 7
	b := make([]byte, sectionsOffset+3*64)
	copy(b, coreNativeFixture("linux", arch))
	binary.LittleEndian.PutUint64(b[32:], 64)
	binary.LittleEndian.PutUint64(b[40:], uint64(sectionsOffset))
	binary.LittleEndian.PutUint16(b[56:], uint16(programs))
	binary.LittleEndian.PutUint16(b[60:], 3)
	program := func(index int, kind elf.ProgType, offset, size int) {
		p := b[64+index*56:]
		binary.LittleEndian.PutUint32(p, uint32(kind))
		binary.LittleEndian.PutUint32(p[4:], uint32(elf.PF_R))
		binary.LittleEndian.PutUint64(p[8:], uint64(offset))
		binary.LittleEndian.PutUint64(p[16:], base+uint64(offset))
		binary.LittleEndian.PutUint64(p[32:], uint64(size))
		binary.LittleEndian.PutUint64(p[40:], uint64(size))
	}
	program(0, elf.PT_LOAD, 0, len(b))
	program(1, elf.PT_DYNAMIC, dynamicOffset, 64)
	if loader != "" {
		program(2, elf.PT_INTERP, loaderOffset, len(loaderData))
	}
	for index, pair := range [][2]uint64{{uint64(tag), 1}, {uint64(elf.DT_STRTAB), base + uint64(stringsOffset)}, {uint64(elf.DT_STRSZ), uint64(len(stringsData))}, {uint64(elf.DT_NULL), 0}} {
		entry := b[dynamicOffset+index*16:]
		binary.LittleEndian.PutUint64(entry, pair[0])
		binary.LittleEndian.PutUint64(entry[8:], pair[1])
	}
	copy(b[stringsOffset:], stringsData)
	copy(b[loaderOffset:], loaderData)
	section := func(index int, kind elf.SectionType, offset, size int) []byte {
		p := b[sectionsOffset+index*64:]
		binary.LittleEndian.PutUint32(p[4:], uint32(kind))
		binary.LittleEndian.PutUint64(p[8:], uint64(elf.SHF_ALLOC))
		binary.LittleEndian.PutUint64(p[16:], base+uint64(offset))
		binary.LittleEndian.PutUint64(p[24:], uint64(offset))
		binary.LittleEndian.PutUint64(p[32:], uint64(size))
		return p
	}
	dynamic := section(1, elf.SHT_DYNAMIC, dynamicOffset, 64)
	binary.LittleEndian.PutUint32(dynamic[40:], 2)
	binary.LittleEndian.PutUint64(dynamic[56:], 16)
	section(2, elf.SHT_STRTAB, stringsOffset, len(stringsData))
	return b
}

func coreMachOLoadFixture(arch string, kind uint32, value string) []byte {
	b := append([]byte(nil), coreNativeFixture("darwin", arch)...)
	size := (12 + len(value) + 1 + 7) &^ 7
	binary.LittleEndian.PutUint32(b[16:], 1)
	binary.LittleEndian.PutUint32(b[20:], uint32(size))
	load := make([]byte, size)
	binary.LittleEndian.PutUint32(load, kind)
	binary.LittleEndian.PutUint32(load[4:], uint32(size))
	binary.LittleEndian.PutUint32(load[8:], 12)
	copy(load[12:], value)
	return append(b, load...)
}

func TestCoreNativeProgramRejectsEmbeddedLoaderOverrides(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			for _, tag := range []elf.DynTag{elf.DT_AUDIT, elf.DT_DEPAUDIT, elf.DT_FILTER, elf.DT_AUXILIARY} {
				t.Run(tag.String(), func(t *testing.T) {
					if err := nativeProgram(coreDynamicELFFixture(arch, tag, "/external/injected.so", ""), "linux/"+arch); err == nil {
						t.Fatal("embedded ELF loader extension accepted")
					}
				})
			}
			for _, value := range []string{"DYLD_LIBRARY_PATH=/external", "DYLD_FRAMEWORK_PATH=@executable_path/private"} {
				if err := nativeProgram(coreMachOLoadFixture(arch, 0x27, value), "darwin/"+arch); err == nil {
					t.Fatal("embedded Mach-O loader environment accepted")
				}
			}
		})
	}
}

func TestCoreNativeProgramRetainsSystemDependencyAndStaticFormats(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		for _, platform := range []string{"linux", "darwin"} {
			if err := nativeProgram(coreNativeFixture(platform, arch), platform+"/"+arch); err != nil {
				t.Fatalf("static format rejected: %s/%s: %v", platform, arch, err)
			}
		}
		for _, dependency := range []struct {
			tag   elf.DynTag
			value string
		}{{elf.DT_NEEDED, "libc.so.6"}, {elf.DT_NEEDED, "/usr/lib/libsafe.so"}, {elf.DT_RPATH, "/lib:/usr/lib"}, {elf.DT_RUNPATH, "/lib64"}} {
			if err := nativeProgram(coreDynamicELFFixture(arch, dependency.tag, dependency.value, "/lib64/ld-linux.so.2"), "linux/"+arch); err != nil {
				t.Fatalf("system ELF dependency rejected: %s %s: %v", arch, dependency.tag, err)
			}
		}
		for _, dependency := range []struct {
			kind  uint32
			value string
		}{{0xe, "/usr/lib/dyld"}, {0x8000001c, "/System/Library/Frameworks"}} {
			if err := nativeProgram(coreMachOLoadFixture(arch, dependency.kind, dependency.value), "darwin/"+arch); err != nil {
				t.Fatalf("system Mach-O dependency rejected: %s: %v", arch, err)
			}
		}
	}
}

func TestCoreNativeProgramRejectsDisagreeingELFDependencyMetadata(t *testing.T) {
	for _, mutation := range []string{"dynamic-range", "string-address", "string-offset", "string-size", "string-index", "duplicate-segment", "duplicate-section"} {
		t.Run(mutation, func(t *testing.T) {
			dependency := "libc.so.6"
			if mutation == "dynamic-range" || strings.HasPrefix(mutation, "string-a") || mutation == "string-offset" {
				dependency = "/external/injected.so"
			}
			data := coreDynamicELFFixture("amd64", elf.DT_NEEDED, dependency, "/lib64/ld-linux.so.2")
			sectionOffset := int(binary.LittleEndian.Uint64(data[40:]))
			dynamicOffset := int(binary.LittleEndian.Uint64(data[64+56+8:]))
			dynamic := data[sectionOffset+64 : sectionOffset+128]
			stringsSection := data[sectionOffset+128 : sectionOffset+192]
			switch mutation {
			case "dynamic-range":
				// The runtime still sees NEEDED=/external/injected.so. The
				// section reader sees the same string as an unused SONAME.
				benign := append([]byte(nil), data[dynamicOffset:dynamicOffset+64]...)
				binary.LittleEndian.PutUint64(benign, uint64(elf.DT_SONAME))
				binary.LittleEndian.PutUint64(dynamic[24:], uint64(len(data)))
				data = append(data, benign...)
			case "string-address", "string-offset":
				benign := []byte("\x00libc.so.6\x00")
				binary.LittleEndian.PutUint64(stringsSection[24:], uint64(len(data)))
				binary.LittleEndian.PutUint64(stringsSection[32:], uint64(len(benign)))
				if mutation == "string-address" {
					binary.LittleEndian.PutUint64(stringsSection[16:], 0x400000+uint64(len(data)))
				}
				data = append(data, benign...)
			case "string-size":
				binary.LittleEndian.PutUint64(data[dynamicOffset+40:], 1)
			case "string-index":
				binary.LittleEndian.PutUint64(data[dynamicOffset+8:], 1<<32)
			case "duplicate-segment":
				copy(data[64+2*56:64+3*56], data[64+56:64+2*56])
			case "duplicate-section":
				binary.LittleEndian.PutUint16(data[60:], 4)
				data = append(data, dynamic...)
			}
			if err := nativeProgram(data, "linux/amd64"); err == nil {
				t.Fatal("ELF section metadata concealed a runtime dependency")
			}
		})
	}
}

func TestCoreArtifactLoaderOverridesCannotExecuteReplaceOrRemoveData(t *testing.T) {
	for _, format := range []string{"elf-audit", "macho-environment"} {
		for _, action := range []string{"install", "update", "remove"} {
			t.Run(format+"/"+action, func(t *testing.T) {
				s, runner, artifact := coreService(t)
				s.Platform = "linux/amd64"
				unsafe := coreDynamicELFFixture("amd64", elf.DT_AUDIT, "/external/injected.so", "")
				if format == "macho-environment" {
					s.Platform = "darwin/arm64"
					unsafe = coreMachOLoadFixture("arm64", 0x27, "DYLD_LIBRARY_PATH=/external")
				}
				goos, arch, _ := strings.Cut(s.Platform, "/")
				original := coreNativeFixture(goos, arch)
				if err := os.WriteFile(artifact, original, 0700); err != nil {
					t.Fatal(err)
				}
				if action != "install" {
					if err := s.Install(artifact, ""); err != nil {
						t.Fatal(err)
					}
				}
				dir, program, _ := s.paths()
				if action == "remove" {
					if err := os.WriteFile(program, unsafe, 0700); err != nil {
						t.Fatal(err)
					}
					original = unsafe
				} else if err := os.WriteFile(artifact, unsafe, 0700); err != nil {
					t.Fatal(err)
				}
				commands := len(runner.commands)
				var err error
				switch action {
				case "install":
					err = s.Install(artifact, "")
				case "update":
					err = s.Update(artifact, "")
				case "remove":
					err = s.Remove()
				}
				if err == nil || len(runner.commands) != commands || runner.starts != 0 || runner.stops != 0 {
					t.Fatalf("loader override reached execution or mutation: %v", err)
				}
				current, err := os.ReadFile(program)
				if action == "install" {
					if !os.IsNotExist(err) {
						t.Fatal("invalid artifact left an installed program")
					}
				} else if err != nil || !bytes.Equal(current, original) {
					t.Fatal("existing executable data changed")
				}
				if matches, err := filepath.Glob(filepath.Join(dir, ".nido-core-*")); err != nil || len(matches) != 0 {
					t.Fatalf("rejected artifact left temporary executables: %v %v", matches, err)
				}
			})
		}
	}
}
