package distribution

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestNativeAPIAcceptanceWorkflowsKeepPublicAndLocalScopes(t *testing.T) {
	type step struct {
		Name, ID, Uses, Run, If string
		Env                     map[string]string
		With                    map[string]any
	}
	type workflow struct {
		On          map[string]any
		Permissions map[string]string
		Jobs        map[string]struct {
			RunsOn string `yaml:"runs-on"`
			Steps  []step
		}
	}
	read := func(name string) workflow {
		t.Helper()
		data, err := os.ReadFile("../../.github/workflows/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var value workflow
		if err := yaml.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	public := read("nido-api-acceptance.yml")
	if len(public.On) != 1 || public.On["workflow_dispatch"] == nil || len(public.Permissions) != 1 || public.Permissions["contents"] != "read" || public.Jobs["acceptance"].RunsOn != "ubuntu-24.04" {
		t.Fatal("public API acceptance must be independent, manual, read-only and native Linux")
	}
	dispatch, _ := public.On["workflow_dispatch"].(map[string]any)
	inputs, _ := dispatch["inputs"].(map[string]any)
	network, _ := inputs["public_network"].(map[string]any)
	if network["type"] != "boolean" || network["default"] != false {
		t.Fatal("real public network acceptance must default to disabled")
	}
	allowed := map[string]bool{
		"actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd":          true,
		"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e":          true,
		"sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6": true,
		"actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02":   true,
	}
	var metadata, signatures, previous, upgrade, installer string
	request, checkout, harnessCheckout, signedScope, uploaded := false, false, false, false, false
	tokenSteps := map[string]bool{}
	for _, item := range public.Jobs["acceptance"].Steps {
		if item.Uses != "" && !allowed[item.Uses] {
			t.Fatal("unreviewed or unpinned acceptance action", item.Uses)
		}
		if strings.Contains(item.Run, "${{") || strings.Contains(item.Run, "gh release create") || strings.Contains(item.Run, "git push") {
			t.Fatal("acceptance may not interpolate expressions into shell or publish")
		}
		if item.ID == "request" {
			request = item.Env["REQUEST_VERSION"] == "${{ inputs.version }}" && strings.Contains(item.Run, "$REQUEST_VERSION =~ ^(0|[1-9][0-9]*)")
		}
		if strings.HasPrefix(item.Uses, "actions/checkout@") {
			if fmt.Sprint(item.With["persist-credentials"]) != "false" {
				t.Fatal("acceptance checkouts must not retain repository credentials")
			}
			switch item.With["path"] {
			case "tagged-source":
				checkout = item.With["ref"] == "${{ steps.request.outputs.tag }}"
			case "acceptance-source":
				harnessCheckout = item.With["ref"] == "${{ github.sha }}" && item.If == "inputs.public_network"
			default:
				t.Fatal("unexpected acceptance checkout", item.With)
			}
		}
		if item.Env["GH_TOKEN"] != "" {
			if (item.ID != "release" && item.ID != "previous_release" && item.ID != "public_upgrade") || item.Env["GH_TOKEN"] != "${{ github.token }}" || tokenSteps[item.ID] {
				t.Fatal("repository token must remain in host-side metadata/download step")
			}
			tokenSteps[item.ID] = true
			switch item.ID {
			case "release":
				metadata = item.Run
			case "previous_release":
				previous = item.Run
			case "public_upgrade":
				upgrade = item.Run
			}
			if item.ID != "release" && item.If != "inputs.public_network" {
				t.Fatal("public HTTPS checks must be explicitly opted in")
			}
		}
		if item.Name == "Verify signatures, release source, package identity and fixture build" {
			signatures = item.Run
		}
		if strings.Contains(item.Run, "nido-public-install-isolation.sh") {
			if item.If != "inputs.public_network" || item.Env["GH_TOKEN"] != "" {
				t.Fatal("public installer must be opt-in and receive no repository token")
			}
			installer = item.Run
		}
		if strings.Contains(item.Run, "bash tests/nido-api-release-isolation.sh") {
			signedScope = item.Env["NIDO_API_RELEASE_INPUT_SCOPE"] == "verified-public" && !strings.Contains(item.Run, "pre-release-local")
		}
		if strings.HasPrefix(item.Uses, "actions/upload-artifact@") {
			uploaded = item.If == "always()"
		}
	}
	if !request || !checkout || !harnessCheckout || !signedScope || !uploaded || len(tokenSteps) != 3 {
		t.Fatal("missing version/tag gate, public-only scope, token boundary or failure evidence")
	}
	for _, required := range []string{"$PREVIOUS_VERSION =~ ^(0|[1-9][0-9]*)", ".draft == false", ".prerelease == false", ".digest", "sha256sum", "--certificate-identity", "--certificate-oidc-issuer", "--certificate-github-workflow-sha", "--certificate-github-workflow-ref", "--certificate-github-workflow-repository"} {
		if !strings.Contains(previous, required) {
			t.Fatal("missing previous public release metadata/signature gate", required)
		}
	}
	for _, required := range []string{"NIDO_PUBLIC_INSTALL_INPUT=\"$evidence/assets\"", "NIDO_PUBLIC_INSTALL_COMMIT=\"$RELEASE_COMMIT\"", "./tests/fixtures/nido-public-install", "$GITHUB_WORKSPACE/acceptance-source/tests/nido-public-install-isolation.sh"} {
		if !strings.Contains(installer, required) {
			t.Fatal("missing signed public installer input or tagged PTY fixture", required)
		}
	}
	for _, required := range []string{"NIDO_ACCEPT_INPUT=\"$evidence/assets\"", "NIDO_ACCEPT_OLD_INPUT=\"$evidence/public-network/previous-assets\"", "NIDO_ACCEPT_OLD_VERSION=\"$PREVIOUS_VERSION\"", "NIDO_ACCEPT_MODE=docker", "NIDO_ACCEPT_METADATA_AUTH=gh", "$GITHUB_WORKSPACE/acceptance-source/tests/nido-public-release-isolation.sh"} {
		if !strings.Contains(upgrade, required) {
			t.Fatal("missing verified previous-version upgrade isolation boundary", required)
		}
	}
	if strings.Contains(upgrade, "-e GH_TOKEN") || strings.Contains(upgrade, "--env GH_TOKEN") {
		t.Fatal("host metadata token must never be forwarded into an acceptance container")
	}
	for _, required := range []string{".draft == false", ".prerelease == false", ".assets | length == 6", "^sha256:[0-9a-f]{64}$", "git/ref/tags/", "[[ $kind = commit && $object = \"$commit\" ]]", "sha256sum", fmt.Sprintf("elif .name == \"install.sh\" then .size <= %d", maxInstaller)} {
		if !strings.Contains(metadata, required) {
			t.Fatal("missing public metadata boundary", required)
		}
	}
	for _, required := range []string{"--certificate-identity", "--certificate-oidc-issuer", "--certificate-github-workflow-sha", "--certificate-github-workflow-ref", "--certificate-github-workflow-repository", "go run ./cmd/nido-dist installer --out \"$evidence/assets\" --verify", "go run ./cmd/nido-dist verify", "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1"} {
		if !strings.Contains(signatures, required) {
			t.Fatal("missing signature/source/package gate", required)
		}
	}
	local, localUpload := false, false
	for _, item := range read("check.yml").Jobs["go"].Steps {
		if strings.Contains(item.Run, "bash tests/nido-api-release-isolation.sh") {
			local = item.If == "matrix.go == '1.27.2'" && strings.Contains(item.Run, "NIDO_API_RELEASE_INPUT_SCOPE=pre-release-local") && strings.Contains(item.Run, "docker pull --platform linux/amd64") && item.Env["GH_TOKEN"] == ""
		}
		if strings.HasPrefix(item.Uses, "actions/upload-artifact@") {
			localUpload = item.If == "always() && matrix.go == '1.27.2'" && allowed[item.Uses]
		}
	}
	if !local || !localUpload {
		t.Fatal("pinned-Go native pre-release contract and its failure evidence are required")
	}
}
