package routine

// builtinPrefix names the built-in routine that wraps one gate. The suffix
// is the gate's own name, which is what lets a project disabling
// `gate-format` drop the format gate from the change pipeline.
const builtinPrefix = "gate-"

// BuiltinName is the routine name that wraps the gate called gate.
func BuiltinName(gate string) string { return builtinPrefix + gate }

// builtinDefinitions are the gates DESIGN.md ships as routines. Their
// trigger is manual because the change trigger for a gate is the gate
// pipeline itself, which already debounces and selects; running them here
// too would double every check on every edit. Naming one in ".wavez.pkl"
// replaces it, and disabling one drops the gate from that pipeline.
var builtinDefinitions = map[string]Definition{
	BuiltinName("format"):     builtinGate("format"),
	BuiltinName("convention"): builtinGate("convention"),
	BuiltinName("lsp"):        builtinGate("lsp"),
	BuiltinName("go-test"):    builtinGate("go-test"),
	BuiltinName("build"):      builtinGate("build"),
}

func builtinGate(gate string) Definition {
	return Definition{
		Name:     BuiltinName(gate),
		Triggers: []Trigger{TriggerManual},
		Steps:    []StepDef{{Name: gate, Action: GatePrefix + gate}},
		Enabled:  true,
	}
}
