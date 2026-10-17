package builtin

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestCatalogProvidesStableNamesWithoutEnablingComponents(t *testing.T) {
	want := []string{"proxy", "gds"}
	names := Names()
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("unexpected catalog: %v", names)
	}
	names[0] = "changed-by-caller"
	if !reflect.DeepEqual(Names(), want) || Known("changed-by-caller") || Known("codex") {
		t.Fatal("catalog was mutated or external Codex was treated as built-in")
	}
	m := Manager{Root: t.TempDir(), Runner: &fakeRunner{}}
	root, err := filepath.EvalSymlinks(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range Names() {
		if !Known(name) {
			t.Fatalf("catalog name %s is not recognized", name)
		}
		component, _ := definition(name)
		_, program, err := m.paths(name)
		if err != nil || program != filepath.Join(root, name, component.Program) {
			t.Fatalf("catalog did not define %s program location", name)
		}
		if status := m.Status(name); status.Installed || status.Configured || status.Running {
			t.Fatalf("catalog implicitly enabled %s: %+v", name, status)
		}
	}
	if err := m.Install("gds", "", ""); err == nil {
		t.Fatal("catalog caused optional GDS to install without an explicit artifact")
	}
}
