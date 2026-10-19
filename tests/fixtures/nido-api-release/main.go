// This test-only executable has the Mihomo CLI shape required by Nido's local
// install/config/start paths. It is not Mihomo and is not a general proxy: its
// CONNECT destinations are the public API/file names, always mapped to one
// private loopback TLS fixture. It never opens external connections.
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type configuration struct {
	Listeners []struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Listen string `json:"listen"`
		Port   int    `json:"port"`
	} `json:"listeners"`
	Rules      []string `json:"rules"`
	Origin     string   `json:"nido-test-api-origin"`
	Controller string   `json:"external-controller,omitempty"`
	Secret     string   `json:"secret,omitempty"`
}

type recorder struct {
	mu   sync.Mutex
	file *os.File
}

func recordFile(path string) (*recorder, error) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) {
		file.Close()
		return nil, fmt.Errorf("fixture log must be a private owned regular file")
	}
	return &recorder{file: file}, nil
}

func (r *recorder) add(value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := json.NewEncoder(r.file).Encode(value); err != nil {
		log.Fatalf("fixture cannot record a request: %v", err)
	}
}

func loopback(address string) bool {
	host, port, err := net.SplitHostPort(address)
	number, portErr := strconv.Atoi(port)
	return err == nil && portErr == nil && host == "127.0.0.1" && number > 0 && number <= 65535
}

func parseConfig(data []byte) (configuration, error) {
	var config configuration
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if len(data) == 0 || len(data) > 4<<20 || decoder.Decode(&config) != nil {
		return config, fmt.Errorf("fixture only accepts its JSON-as-YAML configuration")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || len(config.Listeners) != 1 || len(config.Rules) != 1 || config.Rules[0] != "MATCH,DIRECT" || !loopback(config.Origin) {
		return config, fmt.Errorf("invalid isolated fixture configuration")
	}
	listener := config.Listeners[0]
	if listener.Name != "release-api" || listener.Type != "http" || listener.Listen != "127.0.0.1" || listener.Port < 1024 || listener.Port > 65535 {
		return config, fmt.Errorf("fixture requires its single unprivileged named loopback listener")
	}
	if config.Controller != "" && (!loopback(config.Controller) || config.Secret != "isolated-controller-secret") || config.Controller == "" && config.Secret != "" {
		return config, fmt.Errorf("fixture controller requires its explicit loopback authority")
	}
	return config, nil
}

const revisionMarker = "\nNIDO_RELEASE_FIXTURE_REVISION="

func fixtureRevision(data []byte) int {
	if offset := bytes.LastIndex(data, []byte(revisionMarker)); offset >= 0 {
		value, err := strconv.Atoi(strings.TrimSpace(string(data[offset+len(revisionMarker):])))
		if err == nil && value > 1 && value < 100 {
			return value
		}
	}
	return 1
}

func fixtureVersion() string {
	data, _ := os.ReadFile(os.Args[0])
	return fmt.Sprintf("v0.0.%d", fixtureRevision(data))
}

// fakeNativeUpgrade can modify only the trusted fixture installed as mihomo in
// this run's private component directory. It models upstream replacement and
// same-PID exec, never Nido's retired downloader or a general filesystem updater.
func fakeNativeUpgrade(directory string) (string, error) {
	program := filepath.Join(directory, "mihomo")
	actual, err := filepath.EvalSymlinks(os.Args[0])
	if err != nil || actual != program {
		return "", fmt.Errorf("native fixture upgrade requires its private managed executable")
	}
	info, err := os.Lstat(program)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 128<<20 {
		return "", fmt.Errorf("native fixture program must be private and ordinary")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return "", fmt.Errorf("native fixture program ownership is invalid")
	}
	data, err := os.ReadFile(program)
	if err != nil {
		return "", err
	}
	update := filepath.Join(directory, "meta-update")
	if err := os.Mkdir(update, 0700); err != nil {
		return "", err
	}
	defer os.RemoveAll(update)
	backup := filepath.Join(directory, "meta-backup")
	if err := os.Mkdir(backup, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	backupInfo, err := os.Lstat(backup)
	if err != nil || !backupInfo.IsDir() || backupInfo.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("native fixture backup must be a private ordinary directory")
	}
	entries, err := os.ReadDir(backup)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.Name() != "mihomo" {
			return "", fmt.Errorf("native fixture preserves unknown backup content")
		}
	}
	backupFile := filepath.Join(backup, "mihomo")
	if existing, err := os.Lstat(backupFile); err == nil {
		owner, ok := existing.Sys().(*syscall.Stat_t)
		if !existing.Mode().IsRegular() || existing.Mode().Perm()&0077 != 0 || !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
			return "", fmt.Errorf("native fixture backup is not private and ordinary")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.WriteFile(backupFile, data, 0700); err != nil {
		return "", err
	}
	next := append(append([]byte(nil), data...), []byte(fmt.Sprintf("%s%d\n", revisionMarker, fixtureRevision(data)+1))...)
	candidate := filepath.Join(update, "mihomo")
	if err := os.WriteFile(candidate, next, 0700); err != nil {
		return "", err
	}
	if err := os.Rename(candidate, program); err != nil {
		return "", err
	}
	return program, nil
}

func fixtureController(config configuration, directory, configPath string) (net.Listener, error) {
	listener, err := net.Listen("tcp4", config.Controller)
	if err != nil {
		return nil, err
	}
	requests, err := recordFile(filepath.Join(directory, "nido-test-controller.jsonl"))
	if err != nil {
		listener.Close()
		return nil, err
	}
	reexec := func(program string) {
		go func() {
			time.Sleep(100 * time.Millisecond)
			if err := syscall.Exec(program, append([]string{program}, os.Args[1:]...), os.Environ()); err != nil {
				log.Fatalf("fixture native reexec failed: %v", err)
			}
		}()
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.add(map[string]string{"method": request.Method, "path": request.URL.RequestURI()})
		if request.Header.Get("Authorization") != "Bearer "+config.Secret || request.Header.Get("Cookie") != "" {
			http.Error(w, "fixture controller authorization rejected", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.RequestURI() {
		case "GET /version":
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": true, "version": fixtureVersion()})
		case "GET /proxies":
			_, _ = io.WriteString(w, `{"proxies":{"fixture-group":{"type":"Selector","now":"DIRECT","all":["DIRECT"]},"DIRECT":{"type":"Direct","history":[]}}}`)
		case "GET /providers/proxies":
			_, _ = io.WriteString(w, `{"providers":{"fixture-proxy":{"type":"Proxy","vehicleType":"HTTP"}}}`)
		case "GET /providers/rules":
			_, _ = io.WriteString(w, `{"providers":{"fixture-rule":{"type":"Rule","vehicleType":"HTTP"}}}`)
		case "PUT /providers/proxies/fixture-proxy", "PUT /providers/rules/fixture-rule":
			w.WriteHeader(http.StatusNoContent)
		case "PUT /configs?force=true":
			var body struct {
				Path string `json:"path"`
			}
			data, err := io.ReadAll(io.LimitReader(request.Body, 4097))
			if err != nil || len(data) > 4096 || json.Unmarshal(data, &body) != nil || body.Path != configPath {
				http.Error(w, "fixture reload path rejected", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			reexec(os.Args[0])
		case "POST /upgrade?channel=release":
			mode, _ := os.ReadFile(filepath.Join(directory, "nido-test-native-mode"))
			switch strings.TrimSpace(string(mode)) {
			case "reject":
				http.Error(w, "fixture upgrade rejected", http.StatusServiceUnavailable)
			case "malformed":
				_, _ = io.WriteString(w, `{"status":"unconfirmed"}`)
			case "ack-noop":
				_, _ = io.WriteString(w, `{"status":"ok"}`)
			case "drop-response":
				connection, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = connection.Close()
				}
			default:
				program, err := fakeNativeUpgrade(directory)
				if err != nil {
					http.Error(w, "fixture native upgrade failed", http.StatusInternalServerError)
					return
				}
				_, _ = io.WriteString(w, `{"status":"ok"}`)
				reexec(program)
			}
		default:
			http.NotFound(w, request)
		}
	})}
	go func() { _ = server.Serve(listener); _ = requests.file.Close() }()
	return listener, nil
}

func core(args []string) error {
	if len(args) == 1 && args[0] == "-v" {
		fmt.Println("Mihomo Meta " + fixtureVersion() + " nido-api-release-test-fixture")
		return nil
	}
	validate := false
	if len(args) == 5 && args[0] == "-t" {
		validate, args = true, args[1:]
	}
	if len(args) != 4 || args[0] != "-d" || args[2] != "-f" || !filepath.IsAbs(args[1]) {
		return fmt.Errorf("fixture accepts only -v, -t -d DIR -f -, or -d DIR -f FILE")
	}
	var data []byte
	var err error
	if validate && args[3] == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, 4<<20+1))
	} else if !validate && filepath.IsAbs(args[3]) && filepath.Dir(args[3]) == args[1] {
		info, statErr := os.Lstat(args[3])
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4<<20 {
			return fmt.Errorf("fixture configuration must be a private regular file")
		}
		data, err = os.ReadFile(args[3])
	} else {
		return fmt.Errorf("fixture configuration path is outside its private directory")
	}
	if err != nil {
		return err
	}
	config, err := parseConfig(data)
	if err != nil || validate {
		return err
	}
	requests, err := recordFile(filepath.Join(args[1], "nido-test-connect.jsonl"))
	if err != nil {
		return err
	}
	defer requests.file.Close()
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(config.Listeners[0].Port)))
	if err != nil {
		return err
	}
	defer listener.Close()
	if config.Controller != "" {
		controller, err := fixtureController(config, args[1], args[3])
		if err != nil {
			return err
		}
		defer controller.Close()
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.add(map[string]any{"method": request.Method, "target": request.Host})
		if request.Method != http.MethodConnect || request.Host != "api.github.com:443" && request.Host != "github.com:443" {
			http.Error(w, "fixture allows only GitHub API/file CONNECT", http.StatusForbidden)
			return
		}
		upstream, err := net.DialTimeout("tcp4", config.Origin, 5*time.Second)
		if err != nil {
			http.Error(w, "fixture origin unavailable", http.StatusBadGateway)
			return
		}
		connection, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		defer connection.Close()
		defer upstream.Close()
		if _, err := buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil || buffer.Flush() != nil {
			return
		}
		finished := make(chan struct{})
		go func() {
			_, _ = io.Copy(upstream, buffer)
			if tcp, ok := upstream.(*net.TCPConn); ok {
				_ = tcp.CloseWrite()
			}
			close(finished)
		}()
		_, _ = io.Copy(connection, upstream)
		_ = connection.Close()
		_ = upstream.Close()
		<-finished
	})}
	return server.Serve(listener)
}

func origin(directory, version string) error {
	if !filepath.IsAbs(directory) || !strings.Contains(version, ".") {
		return fmt.Errorf("origin requires a private absolute directory and release version")
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Nido release acceptance private CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		return err
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Nido private route fixture"}, DNSNames: []string{"api.github.com", "github.com", "gh-proxy.org"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600); err != nil {
		return err
	}
	certificate := tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: key}
	requests, err := recordFile(filepath.Join(directory, "requests.jsonl"))
	if err != nil {
		return err
	}
	defer requests.file.Close()
	address := os.Getenv("NIDO_API_FIXTURE_LISTEN")
	if address == "" {
		address = "127.0.0.1:0"
	}
	if !loopback(address) && address != "127.0.0.1:0" {
		return fmt.Errorf("fixture listen address must be loopback")
	}
	listener, err := tls.Listen("tcp4", address, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	defer listener.Close()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	program, err := os.ReadFile(executable)
	if err != nil {
		return err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(program); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	// Deliberately invalid bytes with the reviewed production asset's exact
	// size. A released binary must reject their fixed SHA before executing
	// any candidate. Successful native lifecycle tests use a local trusted
	// fixture with an explicit checksum instead of weakening this identity.
	asset := make([]byte, 22877876)
	copy(asset, compressed.Bytes())
	assetName := "mihomo-linux-amd64-compatible-v1.19.32.gz"
	assetURL := "https://github.com/MetaCubeX/mihomo/releases/download/v1.19.32/" + assetName
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		sni := ""
		if request.TLS != nil {
			sni = request.TLS.ServerName
		}
		route := "direct"
		source := "https://" + request.Host + request.URL.RequestURI()
		prefixFailure := false
		if request.Host == "gh-proxy.org" {
			route = "github-prefix"
			prefixFailure = strings.HasPrefix(request.URL.Path, "/fail/")
			source = strings.TrimPrefix(request.URL.RequestURI(), "/")
			if prefixFailure {
				source = strings.TrimPrefix(source, "fail/")
			}
		}
		requests.add(map[string]string{"method": request.Method, "host": request.Host, "sni": sni, "path": request.URL.Path, "source": source, "route": route, "user_agent": request.UserAgent(), "accept": request.Header.Get("Accept")})
		parsed, parseErr := url.Parse(source)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Host != "api.github.com" && parsed.Host != "github.com" || request.Host == "gh-proxy.org" && parsed.Host != "github.com" || parsed.RawQuery != "" || parsed.User != nil || request.Method != http.MethodGet || sni != request.Host || request.UserAgent() != "Nido" || request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("Authorization") != "" || request.Header.Get("Proxy-Authorization") != "" || request.Header.Get("Cookie") != "" {
			http.Error(w, "unexpected API request identity", http.StatusBadRequest)
			return
		}
		mode, _ := os.ReadFile(filepath.Join(directory, "response-mode"))
		if prefixFailure || strings.TrimSpace(string(mode)) == "deny" {
			http.Error(w, "explicit fixture failure without another route", http.StatusServiceUnavailable)
			return
		}
		if source == assetURL {
			data := asset
			if strings.TrimSpace(string(mode)) == "bad-size" {
				data = append(append([]byte(nil), asset...), 0)
			} else if strings.TrimSpace(string(mode)) == "bad-asset" {
				data = append([]byte(nil), asset...)
				data[0] ^= 0xff
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = w.Write(data)
			return
		}
		var tag string
		switch parsed.Host + parsed.Path {
		case "api.github.com/repos/MiracleHYH/CDEToolkit/releases/latest":
			tag = "v" + version
		case "api.github.com/repos/MetaCubeX/mihomo/releases/latest":
			tag = "v0.0.1"
		case "api.github.com/repos/openai/codex/releases/latest":
			tag = "rust-v0.164.0"
		default:
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		result := map[string]any{"tag_name": tag}
		if strings.TrimSpace(string(mode)) == "bad-tag" {
			result["tag_name"] = "untrusted-tag"
		}
		_ = json.NewEncoder(w).Encode(result)
	})}
	if err := os.WriteFile(filepath.Join(directory, "origin.addr"), []byte(listener.Addr().String()+"\n"), 0600); err != nil {
		return err
	}
	return server.Serve(listener)
}

func main() {
	var err error
	if len(os.Args) == 4 && os.Args[1] == "origin" {
		err = origin(os.Args[2], os.Args[3])
	} else {
		err = core(os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nido-api-release test fixture:", err)
		os.Exit(97)
	}
}
