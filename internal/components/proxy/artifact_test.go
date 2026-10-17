package proxy

import (
	"bytes"
	"compress/gzip"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
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
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(coreNativeFixture("linux", "amd64")); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	payload := compressed.Bytes()
	count := new(int)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*count++
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			name := "mihomo-linux-amd64-v1-v1.2.4.gz"
			digest := hash(payload)
			size := len(payload)
			address := "https://github.com/MetaCubeX/mihomo/releases/download/v1.2.4/" + name
			if problem == "digest" {
				digest = strings.Repeat("0", 64)
			}
			if problem == "size" {
				size = maxProgram + 1
			}
			if problem == "source" {
				address = "https://untrusted.invalid/" + name
			}
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.2.4", "prerelease": problem == "prerelease", "assets": []any{map[string]any{"name": name, "browser_download_url": address, "digest": "sha256:" + digest, "size": size}}})
			return
		}
		if !strings.HasSuffix(r.URL.Path, ".gz") {
			t.Errorf("unexpected release request %s", r.URL.Path)
		}
		if problem == "download" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write(payload)
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
	return client, server.URL, count
}

func TestCoreOfficialArtifactAndVersionChecksUseIsolatedRelease(t *testing.T) {
	for _, problem := range []string{"", "digest", "size", "source", "prerelease", "download"} {
		t.Run(problem, func(t *testing.T) {
			client, api, count := coreReleaseFixture(t, problem)
			s, r, _ := coreService(t)
			r.run = func(c Command) ([]byte, error) { return []byte("Mihomo Meta v1.2.4"), nil }
			s.Platform, s.HTTP, s.API = "linux/amd64", client, api
			err := s.Install("latest", "")
			if problem == "" && err != nil || problem != "" && err == nil {
				t.Fatalf("release result %v", err)
			}
			if r.starts != 0 || r.stops != 0 {
				t.Fatal("release operation changed runtime")
			}
			if problem != "" {
				if len(r.commands) != 0 {
					t.Fatal("invalid download reached core execution")
				}
				return
			}
			if *count != 2 {
				t.Fatalf("release download requests %d", *count)
			}
			info, err := s.CheckUpdate()
			if err != nil || info.Current != "v1.2.4" || info.Latest != "v1.2.4" || info.Available {
				t.Fatalf("update info %+v %v", info, err)
			}
			if *count != 3 {
				t.Fatalf("check downloaded an asset: %d", *count)
			}
		})
	}
}

func TestCoreOfficialCandidateVersionMustMatchSelectedRelease(t *testing.T) {
	client, api, _ := coreReleaseFixture(t, "")
	s, r, _ := coreService(t)
	s.Platform, s.HTTP, s.API = "linux/amd64", client, api
	r.run = func(c Command) ([]byte, error) { return []byte("Mihomo Meta v1.2.3"), nil }
	if err := s.Install("latest", ""); err == nil {
		t.Fatal("official release committed a candidate reporting a different version")
	}
	_, program, _ := s.paths()
	if _, err := os.Lstat(program); !os.IsNotExist(err) {
		t.Fatalf("mismatched release left an installed program: %v", err)
	}
}

func TestCoreOfficialCandidateVersionMustMatchWholeReportedToken(t *testing.T) {
	for _, version := range []string{"v1.2.4-", "v1.2.4+", "v1.2.4.", "v1.2.4/other"} {
		t.Run(version, func(t *testing.T) {
			client, api, _ := coreReleaseFixture(t, "")
			s, r, _ := coreService(t)
			s.Platform, s.HTTP, s.API = "linux/amd64", client, api
			r.run = func(c Command) ([]byte, error) { return []byte("Mihomo Meta " + version + " linux amd64"), nil }
			if err := s.Install("latest", ""); err == nil {
				t.Fatal("official candidate version was truncated to the expected stable tag")
			}
			_, program, _ := s.paths()
			if _, err := os.Lstat(program); !os.IsNotExist(err) {
				t.Fatalf("mismatched release left an installed program: %v", err)
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

func TestCoreUpdateDoesNotExecuteCandidateReplacedDuringVersionCheck(t *testing.T) {
	s, r, artifact := coreService(t)
	if err := s.Install(artifact, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
		t.Fatal(err)
	}
	calls := 0
	r.run = func(c Command) ([]byte, error) {
		calls++
		if c.Args[0] != "-v" {
			t.Fatal("a replaced candidate was executed for configuration validation")
		}
		if err := os.Rename(c.Program, c.Program+"-original"); err != nil {
			t.Fatal(err)
		}
		coreWrite(t, c.Program, []byte("unknown replacement"))
		return []byte("Mihomo Meta v1.2.4"), nil
	}
	if err := s.Update(artifact, ""); err == nil || calls != 1 {
		t.Fatalf("candidate replacement accepted: calls=%d error=%v", calls, err)
	}
}

func TestCoreUpdateRechecksStoppedBeforeCandidateExecution(t *testing.T) {
	for _, stage := range []string{"download", "version"} {
		t.Run(stage, func(t *testing.T) {
			s, r, artifact := coreService(t)
			if stage == "download" {
				s.Platform = "linux/amd64"
				if err := os.WriteFile(artifact, coreNativeFixture("linux", "amd64"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Install(artifact, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
				t.Fatal(err)
			}
			record := coreStoppedRecord(t, s)
			_, program, _ := s.paths()
			before, _ := os.ReadFile(program)
			activated := false
			activate := func() {
				activated = true
				record.Phase = "running"
				r.process = record.Process
				if err := s.saveRecord(record); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "download" {
				client, api, _ := coreReleaseFixture(t, "")
				base := client.Transport
				client.Transport = coreRoundTripper(func(req *http.Request) (*http.Response, error) {
					response, err := base.RoundTrip(req)
					if err == nil && strings.HasSuffix(req.URL.Path, ".gz") {
						activate()
					}
					return response, err
				})
				s.Platform, s.HTTP, s.API, artifact = "linux/amd64", client, api, "latest"
			} else {
				r.run = func(c Command) ([]byte, error) {
					if c.Args[0] != "-v" {
						t.Fatal("configuration validation ran after component became active")
					}
					activate()
					return []byte("Mihomo Meta v1.2.4"), nil
				}
			}
			commands := len(r.commands)
			if err := s.Update(artifact, ""); err == nil {
				t.Fatal("update committed after component became active")
			}
			if !activated {
				t.Fatal("fixture did not reach the requested transition")
			}
			wantCommands := commands
			if stage == "version" {
				wantCommands++
			}
			if len(r.commands) != wantCommands {
				t.Fatalf("unexpected core execution: got=%d want=%d", len(r.commands), wantCommands)
			}
			if after, _ := os.ReadFile(program); !bytes.Equal(after, before) {
				t.Fatal("active component program was replaced")
			}
			if r.starts != 0 || r.stops != 0 {
				t.Fatal("update controlled a process implicitly")
			}
		})
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

func TestCoreCandidateVersionIdentityAndConfigRejectsKeepCurrentProgram(t *testing.T) {
	for _, failure := range []string{"identity", "version-error", "config"} {
		t.Run(failure, func(t *testing.T) {
			s, r, artifact := coreService(t)
			if err := s.Install(artifact, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.ConfigureBytes([]byte(coreConfig)); err != nil {
				t.Fatal(err)
			}
			_, program, _ := s.paths()
			before, _ := os.ReadFile(program)
			if err := os.WriteFile(artifact, append(append([]byte(nil), before...), 1), 0700); err != nil {
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
			err := s.Update(artifact, "")
			if err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("unsafe/absent candidate error %v", err)
			}
			after, _ := os.ReadFile(program)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected candidate replaced current program")
			}
			matches, _ := filepath.Glob(filepath.Join(filepath.Dir(program), ".nido-core-*"))
			if len(matches) != 0 {
				t.Fatal("candidate temporary file leaked", matches)
			}
		})
	}
}

// These are deliberately non-runnable format fixtures. A single PT_LOAD maps
// the table and string bytes, so both section readers and an ELF loader would
// resolve the same dependency metadata.
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
