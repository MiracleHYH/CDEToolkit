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
	allowed := map[string]bool{
		"actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd":          true,
		"actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e":          true,
		"sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6": true,
		"actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02":   true,
	}
	var metadata, signatures string
	request, checkout, signedScope, uploaded, tokenSteps := false, false, false, false, 0
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
			checkout = item.With["ref"] == "${{ steps.request.outputs.tag }}" && fmt.Sprint(item.With["persist-credentials"]) == "false"
		}
		if item.Env["GH_TOKEN"] != "" {
			tokenSteps++
			if item.ID != "release" || item.Env["GH_TOKEN"] != "${{ github.token }}" {
				t.Fatal("repository token must remain in host-side metadata/download step")
			}
			metadata = item.Run
		}
		if strings.Contains(item.Run, "cosign verify-blob") {
			signatures = item.Run
		}
		if strings.Contains(item.Run, "bash tests/nido-api-release-isolation.sh") {
			signedScope = item.Env["NIDO_API_RELEASE_INPUT_SCOPE"] == "verified-public" && !strings.Contains(item.Run, "pre-release-local")
		}
		if strings.HasPrefix(item.Uses, "actions/upload-artifact@") {
			uploaded = item.If == "always()"
		}
	}
	if !request || !checkout || !signedScope || !uploaded || tokenSteps != 1 {
		t.Fatal("missing version/tag gate, public-only scope, token boundary or failure evidence")
	}
	for _, required := range []string{".draft == false", ".prerelease == false", ".assets | length == 6", "^sha256:[0-9a-f]{64}$", "git/ref/tags/", "[[ $kind = commit && $object = \"$commit\" ]]", "sha256sum"} {
		if !strings.Contains(metadata, required) {
			t.Fatal("missing public metadata boundary", required)
		}
	}
	for _, required := range []string{"--certificate-identity", "--certificate-oidc-issuer", "--certificate-github-workflow-sha", "--certificate-github-workflow-ref", "--certificate-github-workflow-repository", "cmp scripts/install.sh", "go run ./cmd/nido-dist verify", "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1"} {
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
