package core

import "fmt"

// JoinPolicy selects when a Capability with several incoming bindings becomes
// ready to execute.
//
// It is a property of the join at the target Capability. The canonical manifest
// attaches it to each incoming binding so the graph needs no second edge
// structure; every binding that converges on one target must declare the same
// effective policy.
type JoinPolicy string

const (
	// JoinPolicyAll waits for every incoming binding to be satisfied before the
	// target executes. It is the only policy supported today and is the default
	// when a binding declares none.
	JoinPolicyAll JoinPolicy = "all"
)

// Effective returns the policy with its default applied. An unspecified policy
// means JoinPolicyAll, so an author never has to declare the only policy that
// exists.
func (p JoinPolicy) Effective() JoinPolicy {
	if p == "" {
		return JoinPolicyAll
	}
	return p
}

// Validate reports whether the policy is one the runtime can honour. A policy
// that is unsupported must fail compilation rather than silently behaving like
// the default.
func (p JoinPolicy) Validate() error {
	switch p.Effective() {
	case JoinPolicyAll:
		return nil
	default:
		return fmt.Errorf("unknown join policy %q", p)
	}
}
