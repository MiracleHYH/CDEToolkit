package nido

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runCondaEnvironmentScript(t *testing.T, shell, script string) {
	t.Helper()
	cmd := exec.Command(shell, "-c", "set -eu\n"+script)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", shell, err, output)
	}
}
func writeCondaEnvironment(t *testing.T, dir, name string, vars map[string]string, unset ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(condaEnvironment(vars, unset...)), 0600); err != nil {
		t.Fatal(err)
	}
	return ". " + shellQuote(path) + "\n"
}

func TestCondaEnvironmentRestoreAndSwitch(t *testing.T) {
	for _, shell := range []string{"/bin/sh", "/bin/bash", "/bin/zsh"} {
		if _, err := os.Stat(shell); err != nil {
			continue
		}
		t.Run(filepath.Base(shell), func(t *testing.T) {
			dir := t.TempDir()
			first := writeCondaEnvironment(t, dir, "first", map[string]string{"CONDARC": "/managed/first", "PIP_INDEX_URL": "https://first.example/simple", "CONDA_ENVS_PATH": "ignored"}, "CONDA_DEFAULT_CHANNELS")
			second := writeCondaEnvironment(t, dir, "second", map[string]string{"CONDARC": "/managed/default", "PIP_INDEX_URL": "https://original.example/simple", "CONDA_DEFAULT_CHANNELS": "saved-original"})
			reset := writeCondaEnvironment(t, dir, "reset", nil)
			original := "original ' \" $HOME `touch must-not-exist`\nline"
			script := "export CONDARC=" + shellQuote(original) + "\nexport CONDA_DEFAULT_CHANNELS=original-override CONDA_ENVS_PATH=native-user-value PIP_CACHE_DIR=native-cache\nunset PIP_INDEX_URL CONDA_PKGS_DIRS\n" + first + first +
				"[ \"$CONDARC\" = /managed/first ]\n[ \"${CONDA_DEFAULT_CHANNELS+x}\" != x ]\n[ \"$CONDA_ENVS_PATH\" = native-user-value ]\n" + second + second +
				"[ \"$CONDA_DEFAULT_CHANNELS\" = saved-original ]\n[ \"$PIP_INDEX_URL\" = https://original.example/simple ]\n" + reset + reset +
				"[ \"$CONDARC\" = " + shellQuote(original) + " ]\n[ \"$CONDA_DEFAULT_CHANNELS\" = original-override ]\n[ \"${PIP_INDEX_URL+x}${CONDA_PKGS_DIRS+x}\" = '' ]\n[ \"$CONDA_ENVS_PATH\" = native-user-value ]\n[ \"$PIP_CACHE_DIR\" = native-cache ]\n"
			runCondaEnvironmentScript(t, shell, script)
		})
	}
}
func TestCondaEnvironmentUserChangesBecomeNewBaseline(t *testing.T) {
	dir := t.TempDir()
	apply := writeCondaEnvironment(t, dir, "apply", map[string]string{"CONDARC": "managed"}, "CONDA_DEFAULT_CHANNELS")
	reset := writeCondaEnvironment(t, dir, "reset", nil)
	script := "export CONDARC=original CONDA_DEFAULT_CHANNELS=original-override\n" + apply + "CONDARC=user-edit\nCONDA_DEFAULT_CHANNELS=user-override\n" + reset +
		"[ \"$CONDARC\" = user-edit ]\n[ \"$CONDA_DEFAULT_CHANNELS\" = user-override ]\n" + apply + apply + reset +
		"[ \"$CONDARC\" = user-edit ]\n[ \"$CONDA_DEFAULT_CHANNELS\" = user-override ]\n"
	runCondaEnvironmentScript(t, "/bin/sh", script)
}
func TestCondaEnvironmentChildShellCanRestore(t *testing.T) {
	dir := t.TempDir()
	apply := writeCondaEnvironment(t, dir, "apply", map[string]string{"PIP_INDEX_URL": "managed"}, "CONDA_CHANNEL_ALIAS")
	reset := writeCondaEnvironment(t, dir, "reset", nil)
	child := reset + "[ \"$PIP_INDEX_URL\" = original ]\n[ \"$CONDA_CHANNEL_ALIAS\" = original-alias ]\n"
	runCondaEnvironmentScript(t, "/bin/sh", "export PIP_INDEX_URL=original CONDA_CHANNEL_ALIAS=original-alias\n"+apply+"/bin/sh -eu -c "+shellQuote(child)+"\n[ \"$PIP_INDEX_URL\" = managed ]\n[ \"${CONDA_CHANNEL_ALIAS+x}\" != x ]\n")
}
func TestCondaEnvironmentDoesNotExecuteValues(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	value := "' \" $(touch " + marker + ") `touch " + marker + "` $HOME\nline"
	apply := writeCondaEnvironment(t, dir, "apply", map[string]string{"PIP_INDEX_URL": value})
	reset := writeCondaEnvironment(t, dir, "reset", nil)
	runCondaEnvironmentScript(t, "/bin/sh", "unset PIP_INDEX_URL\n"+apply+apply+"[ \"$PIP_INDEX_URL\" = "+shellQuote(value)+" ]\n"+reset+"[ \"${PIP_INDEX_URL+x}\" = '' ]\n[ ! -e "+shellQuote(marker)+" ]\n")
}
func TestCondaEnvironmentEmptyOnlyCleansUpKnownVariables(t *testing.T) {
	empty := condaEnvironment(nil)
	if strings.Contains(empty, "eval") {
		t.Fatal("evaluates shell values")
	}
	if got := condaEnvironment(map[string]string{"ARBITRARY": "value", "CONDA_ENVS_PATH": "ignored", "PIP_CACHE_DIR": "ignored"}); got != empty {
		t.Fatal("manages arbitrary or storage variables")
	}
	runCondaEnvironmentScript(t, "/bin/sh", "export PIP_INDEX_URL=user-value CONDA_ENVS_PATH=envs PIP_CACHE_DIR=cache\n"+empty+"[ \"$PIP_INDEX_URL\" = user-value ]\n[ \"$CONDA_ENVS_PATH\" = envs ]\n[ \"$PIP_CACHE_DIR\" = cache ]\n[ \"${CONDARC+x}\" = '' ]\n")
}
