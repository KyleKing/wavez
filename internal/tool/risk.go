package tool

// RiskClass is the kind of side effect a tool call can have, declared by the
// tool rather than decided by whoever wires it up. The dispatcher reads it on
// every call path and consults the permission gate from the class, so a tool
// that declares a risky class cannot reach Run without passing the check.
type RiskClass string

const (
	// RiskRead touches nothing outside the process: it reads the project
	// and reports back.
	RiskRead RiskClass = "read"
	// RiskEgress sends a request to the network the model chose the
	// destination of.
	RiskEgress RiskClass = "egress"
	// RiskWriteLocal changes files under the project root.
	RiskWriteLocal RiskClass = "write_local"
	// RiskExec runs a process the model named.
	RiskExec RiskClass = "exec"
	// RiskExternal reaches outside the repository and the local process:
	// asking the user, or a service acting on the run's behalf.
	RiskExternal RiskClass = "external"
)
