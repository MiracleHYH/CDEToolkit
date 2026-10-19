package nido

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MiracleHYH/CDEToolkit/internal/terminalui"
)

type proxyPageCapture struct {
	page       terminalui.Page
	status     string
	input      func(string, string) (string, error)
	selections []string
}

func (u *proxyPageCapture) Page(page terminalui.Page) (int, error) {
	if page.Status != nil {
		u.status = page.Status()
	}
	if page.ActionsFunc != nil {
		page.Actions = page.ActionsFunc()
	}
	u.page = page
	return 0, nil
}

func TestProxyPageActionCallbackTracksInstallAndRemovalWithoutAnotherPage(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	runner := &cliProxyRunner{}
	m.Proxy.Runner = runner
	ui := &proxyPageCapture{}
	var output bytes.Buffer
	cli := &CLI{UI: ui, Out: &output, Err: &output}
	if code := cli.proxyPage(m, cliOptions{}); code != 0 || ui.page.ActionsFunc == nil {
		t.Fatal("page did not supply dynamic actions", code, output.String())
	}
	if err := m.Proxy.Install(proxyCLIArtifact(t), ""); err != nil {
		t.Fatal(err)
	}
	installed := ui.page.ActionsFunc()
	if len(installed) != 8 || installed[0].ID != "quickstart" || installed[7].ID != "remove" {
		t.Fatal("installed state kept the installation action", installed)
	}
	if err := m.Proxy.Remove(); err != nil {
		t.Fatal(err)
	}
	removed := ui.page.ActionsFunc()
	if len(removed) != 1 || removed[0].ID != "install" || runner.starts != 0 {
		t.Fatal("removal kept stale component actions or started a core", removed, runner.starts)
	}
}
func (u *proxyPageCapture) Select(label string, _ []string, _ int) (int, error) {
	u.selections = append(u.selections, label)
	return 1, nil
}
func (u *proxyPageCapture) Input(label, fallback string) (string, error) {
	if u.input != nil {
		return u.input(label, fallback)
	}
	return "", fmt.Errorf("unexpected input")
}
func proxyPageActionIDs(actions []menuAction) []string {
	ids := make([]string, 0, len(actions))
	for _, action := range actions {
		ids = append(ids, action.id)
	}
	return ids
}
func TestProxyPageMissingCoreOnlyOffersInstallAndIsReadOnly(t *testing.T) {
	m, _ := fixture(t)
	if err := m.Init(); err != nil {
		t.Fatal(err)
	}
	before := proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)
	ui := &proxyPageCapture{}
	var output bytes.Buffer
	cli := &CLI{UI: ui, Out: &output, Err: &output}
	if code := cli.proxyPage(m, cliOptions{}); code != 0 {
		t.Fatal(code, output.String())
	}
	if len(ui.page.Actions) != 1 || ui.page.Actions[0].ID != "install" || ui.page.Actions[0].Label != "安装 Proxy" || !strings.Contains(ui.status, "config.yaml") || !strings.Contains(ui.status, "output.log") {
		t.Fatal("missing component exposes extra actions or hides native paths", ui.page.Actions, ui.status)
	}
	if !reflect.DeepEqual(before, proxyBootstrapSnapshot(t, "", m.Root, m.UserHome)) {
		t.Fatal("reading a missing page changed files")
	}
}
func TestProxyPageInstalledActionsAreFlatAndContainNoExternalTools(t *testing.T) {
	m, runner, _, _ := proxyRemediationFixture(t, false)
	commands := len(runner.commands)
	cli := &CLI{}
	actions := cli.proxyPageActions(m, cliOptions{})
	want := []string{"quickstart", "update", "provider-update", "reload", "restart", "stop", "start", "remove"}
	if ids := proxyPageActionIDs(actions); !reflect.DeepEqual(ids, want) {
		t.Fatal(ids)
	}
	for _, action := range actions {
		if action.group == "" || action.run == nil {
			t.Fatal("action lacks visual grouping or implementation", action.id)
		}
		for _, forbidden := range []string{"导入", "日志", "工具", "测速", "节点切换", "连接管理"} {
			if strings.Contains(action.label, forbidden) {
				t.Fatal("extra feature on Proxy page", action)
			}
		}
	}
	if len(runner.commands) != commands || runner.starts != 0 || runner.stops != 0 {
		t.Fatal("action discovery executed the core")
	}
}
func TestProxyPageDeletedLiveImageStillOffersStopAndRemove(t *testing.T) {
	m, runner, _, _ := proxyRemediationFixture(t, true)
	if err := os.Remove(filepath.Join(m.Root, "proxy", "mihomo")); err != nil {
		t.Fatal(err)
	}
	cli := &CLI{}
	ids := proxyPageActionIDs(cli.proxyPageActions(m, cliOptions{}))
	if !strings.Contains(strings.Join(ids, ","), "stop,start,remove") || ids[0] == "install" {
		t.Fatal("live missing image lost recovery actions", ids)
	}
	if runner.stops != 0 || runner.starts != 1 {
		t.Fatal("page changed the live instance")
	}
}
func TestProxyPrivateInputDoesNotFallBackToPlainText(t *testing.T) {
	plainCalls := 0
	ui := &proxyPageCapture{input: func(string, string) (string, error) { plainCalls++; return "private-value", nil }}
	cli := &CLI{UI: ui}
	if _, err := cli.secretPrompt("ISP 节点链接", ""); err == nil || plainCalls != 0 {
		t.Fatal("missing common private input exposed credentials", err, plainCalls)
	}
}
func TestProxyRemoveUsesOneConfirmationAndRetainsConfiguration(t *testing.T) {
	m, runner, path, want := proxyRemediationFixture(t, true)
	ui := &proxyPageCapture{}
	var output bytes.Buffer
	cli := &CLI{UI: ui, Out: &output, Err: &output}
	if code := cli.proxy(m, cliOptions{}, []string{"remove"}); code != 0 {
		t.Fatal(code, output.String())
	}
	if !reflect.DeepEqual(ui.selections, []string{"停止并删除 Proxy，保留配置和数据"}) || runner.stops != 1 {
		t.Fatal("remove did not use one combined confirmation", ui.selections, runner.stops)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
		t.Fatal("remove changed configuration", err)
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "proxy", "mihomo")); !os.IsNotExist(err) {
		t.Fatal("remove kept the core", err)
	}
}
