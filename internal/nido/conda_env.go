package nido

import (
	"github.com/MiracleHYH/CDEToolkit/internal/integrations/conda"
	"strings"
)

// Only mirror variables participate in shell restoration. Storage locations are
// edited in native configuration and never restored by source/off/reset.
// Exported private markers carry mirror restoration into child shells; values
// remain opaque strings and no eval or user-provided code is executed.
func condaEnvironment(vars map[string]string, unsetNames ...string) string {
	var b strings.Builder
	clear := map[string]bool{}
	for _, name := range unsetNames {
		clear[name] = true
	}
	b.WriteString("# Apply Nido mirror profiles; native storage paths are persistent.\n")
	for _, name := range append([]string{"CONDARC", "PIP_INDEX_URL"}, conda.MirrorVariableNames()...) {
		prefix := "_NIDO_CONDA_" + name
		active, value, original, set, applied := prefix+"_ACTIVE", prefix+"_VALUE", prefix+"_ORIGINAL", prefix+"_ORIGINAL_SET", prefix+"_APPLIED_SET"
		b.WriteString("if [ \"${" + active + "-}\" = 1 ]; then\n")
		// Missing APPLIED_SET is the old draft's set-value marker. New profiles
		// explicitly record 'x' for assignments and '' for an intentional unset.
		b.WriteString("  if { [ \"${" + applied + "-x}\" = x ] && [ \"${" + name + "+x}\" = x ] && [ \"${" + name + "-}\" = \"${" + value + "-}\" ]; } || { [ \"${" + applied + "-x}\" != x ] && [ \"${" + name + "+x}\" != x ]; }; then\n")
		b.WriteString("    if [ \"${" + set + "-}\" = x ]; then\n      export " + name + "=\"${" + original + "-}\"\n    else\n      unset " + name + "\n    fi\n  fi\n")
		b.WriteString("  unset " + active + " " + value + " " + original + " " + set + " " + applied + "\nfi\n")
		next, assign := vars[name]
		if !assign && !clear[name] {
			continue
		}
		b.WriteString(set + "=\"${" + name + "+x}\"\n" + original + "=\"${" + name + "-}\"\n" + value + "=" + shellQuote(next) + "\n" + active + "=1\n")
		appliedValue := ""
		if assign {
			appliedValue = "x"
		}
		b.WriteString(applied + "=" + shellQuote(appliedValue) + "\nexport " + active + " " + value + " " + original + " " + set + " " + applied + "\n")
		if assign {
			b.WriteString("export " + name + "=" + shellQuote(next) + "\n")
		} else {
			b.WriteString("unset " + name + "\n")
		}
	}
	return b.String()
}
