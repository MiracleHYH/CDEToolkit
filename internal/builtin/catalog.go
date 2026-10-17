package builtin

// Definition describes a built-in's fixed place in Nido. Component-specific
// installation, configuration and runtime behavior stay in ordinary Go code;
// this catalog is not a dynamically loaded plugin or lifecycle framework.
type Definition struct {
	Name        string
	Program     string
	NeedsConfig bool
}

var catalog = []Definition{
	{Name: "gds", Program: "gds"},
}

// Names returns a new slice in stable display order. Inclusion in the catalog
// never installs or enables a component: GDS still requires a local artifact.
func Names() []string {
	names := make([]string, len(catalog))
	for i, component := range catalog {
		names[i] = component.Name
	}
	return names
}

func Known(name string) bool {
	_, ok := definition(name)
	return ok
}

func definition(name string) (Definition, bool) {
	for _, component := range catalog {
		if component.Name == name {
			return component, true
		}
	}
	return Definition{}, false
}
