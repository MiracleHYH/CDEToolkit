package nido

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
	"github.com/creack/pty"
)

func TestCondaContextCancelledCaptureDoesNotQueryNextToolOrWrite(t *testing.T) {
	m, home := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	before := condaInteractiveSnapshot(t, m, home)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Conda.Ctx = ctx
	queries := 0
	m.Conda.Runner = func(name string, args, env []string) ([]byte, error) {
		queries++
		if queries != 1 || name != "conda" {
			t.Fatal("cancelled capture started another query", name)
		}
		data, err := originalMirrorRunner(name, args, env)
		cancel()
		return data, err
	}
	var output bytes.Buffer
	c := CLI{Out: &output, Err: &output}
	if c.conda(m, cliOptions{yes: true}, []string{"setup", "all"}) != 0 || !c.promptClosed || queries != 1 {
		t.Fatal("cancelled capture did not close safely", queries, output.String())
	}
	if m.Conda.Ctx != ctx || !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) || m.Config.Conda.Defaults.CondaCaptured || m.Config.Conda.Defaults.PipCaptured {
		t.Fatal("cancelled capture wrote defaults or replaced the caller context")
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "conda")); !os.IsNotExist(err) {
		t.Fatal("cancelled capture published overlay", err)
	}
}

func TestCondaContextCancellationAtConfirmationPreventsCommit(t *testing.T) {
	for _, action := range []string{"path", "mirror", "setup", "reset"} {
		t.Run(action, func(t *testing.T) {
			m, home := condaInteractiveFixture(t)
			before := condaInteractiveSnapshot(t, m, home)
			defaults := m.Config.Conda.Defaults
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m.Conda.Ctx = ctx
			confirm := condaAccept()
			confirm.before = cancel
			u := newCondaUI(t, confirm)
			var output bytes.Buffer
			c := CLI{UI: u, Out: &output, Err: &output}
			args := []string{"envs", filepath.Join(home, "cancelled-before-commit")}
			switch action {
			case "mirror":
				args = []string{"mirror", "pip", "official"}
			case "setup":
				args = []string{"setup", "all"}
			case "reset":
				args = []string{"reset"}
			}
			if c.conda(m, cliOptions{}, args) != 0 || !c.promptClosed || !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) || !reflect.DeepEqual(defaults, m.Config.Conda.Defaults) {
				t.Fatal("cancelled confirmation committed configuration", output.String())
			}
		})
	}
}

func TestCondaManagerCancelledContextRejectsCommit(t *testing.T) {
	m, home := condaInteractiveFixture(t)
	before := condaInteractiveSnapshot(t, m, home)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.Conda.Ctx = ctx
	next := m.Config.Conda
	next.EnvsDir = filepath.Join(home, "must-not-commit")
	if err := m.ConfigureConda(next); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled ConfigureConda accepted commit", err)
	}
	if err := m.RecordCondaDefaults(next.Defaults); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled RecordCondaDefaults accepted commit", err)
	}
	if !reflect.DeepEqual(before, condaInteractiveSnapshot(t, m, home)) {
		t.Fatal("cancelled manager changed configuration")
	}
}

func TestCondaContextDoesNotHideUnrelatedWriteFailure(t *testing.T) {
	m, _ := condaFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.Conda.Ctx = ctx
	var output bytes.Buffer
	c := CLI{Out: &output, Err: &output}
	if c.condaOperationError(m, errors.New("native write failed")) == 0 || !bytes.Contains(output.Bytes(), []byte("native write failed")) {
		t.Fatal("exit request hid an unrelated write failure")
	}
}

func TestCondaContextSessionInjectionRestoresManagerContext(t *testing.T) {
	m, _ := condaFixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	session, err := terminalui.New(slave, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	previous, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Conda.Ctx = previous
	queried := false
	m.Conda.Runner = func(string, []string, []string) ([]byte, error) {
		queried = true
		if m.Conda.Ctx != session.Context() {
			t.Fatal("command did not inherit session context")
		}
		return nil, errors.New("isolated query failure")
	}
	var output bytes.Buffer
	c := CLI{uiSession: session, Out: &output, Err: &output}
	if c.conda(m, cliOptions{yes: true}, []string{"setup", "conda"}) == 0 || !queried || m.Conda.Ctx != previous {
		t.Fatal("session context was not scoped to this command", output.String())
	}
}
