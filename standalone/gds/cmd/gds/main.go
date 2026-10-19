// GDS is a separately built optional component. The Nido executable never
// imports this entry point or its cgo-dependent NVIDIA adapter.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsruntime"
	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsruntime/nvidia"
	"github.com/MiracleHYH/CDEToolkit/standalone/gds/internal/gdsutil"
)

// Override with -ldflags '-X main.version=...'; protocol is versioned separately.
var version = "0.1.0-dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "gds:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, in io.Reader, out, stderr io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "__helper" {
		if len(args) < 3 || args[1] != "--" || (args[2] != "--version" && args[2] != "probe" && args[2] != "__probe") {
			return fmt.Errorf("helper 仅接受 metadata 或 probe 命令")
		}
		// The manager commits this process's exact kernel identity before
		// writing authorization. In particular, parent death before commit
		// cannot leave an unrecorded native query running in the driver.
		gate := os.NewFile(3, "gds-helper-authorization")
		if gate == nil {
			return fmt.Errorf("missing helper authorization gate")
		}
		if err := readHelperGate(ctx, gate); err != nil {
			return err
		}
		return run(ctx, args[2:], in, out, stderr)
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		_, err := fmt.Fprintln(out, "用法: gds --version [--json] | gds run --dir DIR\nGDS 需要独立构建的 Linux amd64 cgo 程序。内部 probe/worker 模式由 monitor 管理。")
		return err
	}
	if args[0] == "--version" {
		if len(args) > 2 || (len(args) == 2 && args[1] != "--json") {
			return fmt.Errorf("--version 仅接受 --json")
		}
		// Metadata is deliberately independent of installed drivers and CUDA.
		return json.NewEncoder(out).Encode(gdsapi.Metadata{Product: "gds", Version: version, Protocol: gdsapi.ProtocolVersion, Platform: runtime.GOOS + "/" + runtime.GOARCH, Native: nvidia.Available()})
	}
	f := flag.NewFlagSet("gds "+args[0], flag.ContinueOnError)
	f.SetOutput(stderr)
	switch args[0] {
	case "run":
		dir := f.String("dir", "", "GDS 组件的绝对目录")
		managed := f.Bool("managed", false, "等待 FD 3 的一次性 monitor 授权")
		token := f.String("token", "", "本次 monitor 启动标识")
		boot := f.String("boot-id", "", "预期 Linux boot ID")
		stateHash := f.String("state-sha256", "", "父进程已核验的 state 快照摘要，或 absent")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 || *dir == "" || !filepath.IsAbs(*dir) {
			return fmt.Errorf("run 需要 --dir 绝对路径，且不接受位置参数")
		}
		if !nvidia.Available() {
			return fmt.Errorf("GDS 仅支持 Linux amd64 且启用 cgo 的独立组件")
		}
		if *managed && (*token == "" || *boot == "") {
			return fmt.Errorf("受管启动需要 token 与 boot-id")
		}
		data, err := gdsutil.Read(filepath.Join(*dir, "config.toml"), 64<<10)
		if err != nil {
			return err
		}
		config, err := gdsapi.ParseConfig(data)
		if err != nil {
			return err
		}
		return gdsruntime.Run(ctx, gdsruntime.RunOptions{Dir: *dir, Config: config, Token: *token, BootID: *boot, Managed: *managed, ExpectedStateHash: *stateHash})
	case "__probe", "probe":
		uuid := f.String("gpu", "", "完整 GPU UUID")
		publicUUID := f.String("uuid", "", "完整 GPU UUID")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *publicUUID != "" {
			if *uuid != "" {
				return fmt.Errorf("不可同时指定 --gpu 和 --uuid")
			}
			*uuid = *publicUUID
		}
		if f.NArg() != 0 || !gdsapi.ValidUUID(*uuid) {
			return fmt.Errorf("probe 需要完整 GPU UUID")
		}
		if !nvidia.Available() {
			return fmt.Errorf("GDS NVIDIA probe 仅支持 Linux amd64 cgo")
		}
		stopWatchdog, err := startProbeWatchdog(gdsruntime.BootClock{}, gdsruntime.ProbeTimeout+gdsruntime.CleanupTimeout, os.Exit)
		if err != nil {
			return err
		}
		defer stopWatchdog()
		if err := ctx.Err(); err != nil {
			return err
		}
		return gdsruntime.RunProbe(out, nvidia.New(), *uuid)
	case "__worker":
		uuid := f.String("gpu", "", "完整 GPU UUID")
		token := f.String("token", "", "本次 worker 启动标识")
		boot := f.String("boot-id", "", "预期 Linux boot ID")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 || !gdsapi.ValidUUID(*uuid) || *token == "" || *boot == "" {
			return fmt.Errorf("worker 需要完整身份参数")
		}
		if !nvidia.Available() {
			return fmt.Errorf("GDS NVIDIA worker 仅支持 Linux amd64 cgo")
		}
		return gdsruntime.RunWorker(ctx, gdsruntime.WorkerOptions{GPUUUID: *uuid, Token: *token, BootID: *boot, Input: in, Output: out}, nvidia.New())
	default:
		return fmt.Errorf("未知 GDS 命令 %q", args[0])
	}
}

func readHelperGate(ctx context.Context, gate *os.File) error {
	defer gate.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		var authorization [1]byte
		_, err := io.ReadFull(gate, authorization[:])
		if err == nil && authorization[0] != 1 {
			err = fmt.Errorf("helper start denied")
		}
		done <- err
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		// Cancellation has precedence over an authorization that was already
		// queued when both select cases became ready.
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("helper authorization timeout")
	}
}
