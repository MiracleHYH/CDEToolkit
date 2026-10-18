// This test-only executable has the Mihomo CLI shape required by Nido's local
// install/config/start paths. It is not Mihomo and is not a general proxy: its
// sole CONNECT destination is api.github.com:443, mapped to a private loopback
// TLS fixture. It never fetches subscriptions or opens external connections.
package main

import (
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
	Rules  []string `json:"rules"`
	Origin string   `json:"nido-test-api-origin"`
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
	return config, nil
}

func core(args []string) error {
	if len(args) == 1 && args[0] == "-v" {
		fmt.Println("Mihomo Meta v0.0.0 nido-api-release-test-fixture")
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
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.add(map[string]any{"method": request.Method, "target": request.Host})
		if request.Method != http.MethodConnect || request.Host != "api.github.com:443" {
			http.Error(w, "fixture allows only GitHub API CONNECT", http.StatusForbidden)
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
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "api.github.com"}, DNSNames: []string{"api.github.com"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
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
	listener, err := tls.Listen("tcp4", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		sni := ""
		if request.TLS != nil {
			sni = request.TLS.ServerName
		}
		requests.add(map[string]string{"method": request.Method, "host": request.Host, "sni": sni, "path": request.URL.Path, "user_agent": request.UserAgent(), "accept": request.Header.Get("Accept")})
		if request.Method != http.MethodGet || request.Host != "api.github.com" || sni != "api.github.com" || request.UserAgent() != "Nido" || request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
			http.Error(w, "unexpected API request identity", http.StatusBadRequest)
			return
		}
		var tag string
		switch request.URL.Path {
		case "/repos/MiracleHYH/CDEToolkit/releases/latest":
			tag = "v" + version
		case "/repos/MetaCubeX/mihomo/releases/latest":
			tag = "v0.0.1"
		case "/repos/openai/codex/releases/latest":
			tag = "rust-v0.164.0"
		default:
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": tag})
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
