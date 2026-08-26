package firstboot

// What "finished" means, per resource.
//
// A create answers 202 and converges in the background, so a client has to poll
// until the state is one that has settled. There is no way to derive which
// values those are from the schema: OpenAPI can list an enum and has no field
// for "and these three are terminal". The API documents it in each state
// field's description; this file is that documentation as code, and
// state_test.go is what keeps the two from drifting.
//
// # The rule for an unknown value
//
// A state this table does not know is treated as STILL WORKING, never as
// settled. The asymmetry is deliberate and it is the whole safety property: a
// new state added to the API reaches an old client, and an old client that
// guessed "settled" would hand back a server that is not ready -- a caller then
// tries to SSH into a machine mid-provision and reads the failure as ours. An
// old client that guesses "still working" merely waits until its own timeout,
// which is a delay rather than a wrong answer.

// Outcome classifies one state value.
type Outcome int

const (
	// Working: the value will change on its own. Keep polling.
	Working Outcome = iota
	// Ready: the resource reached the state a successful create aims at.
	Ready
	// Settled: terminal, but not what a create was waiting for -- a server that
	// is `stopped`, a database that is `suspended`. Waiting longer is pointless;
	// whether it is a problem is the caller's judgement, not this library's.
	Settled
	// Failed: terminal and a failure. The waiters turn this into a *StateError.
	Failed
)

// resourceStates is the whole table. Anything absent is Working by the rule
// above. Written per resource rather than as one shared map because the same
// word means different things: `active` is success for a network and does not
// exist for a server, and `stopped` is a settled non-failure for a server and
// has no meaning for a volume.
var resourceStates = map[string]map[string]Outcome{
	"server": {
		"running":            Ready,
		"stopped":            Settled,
		"suspended":          Settled,
		"abuse_suspended":    Settled,
		"deleted":            Settled,
		"error_provisioning": Failed,
		"error_resize":       Failed,
		"error_delete":       Failed,
		"error_rebuild":      Failed,
		"error_reconfigure":  Failed,
		"error_restore":      Failed,
	},
	"volume": {
		"available": Ready,
		// A volume created with a server attached settles attached, so both are
		// a successful end of a create rather than one being a lesser outcome.
		"attached": Ready,
		"error":    Failed,
	},
	"network": {
		"active": Ready,
		"error":  Failed,
	},
	"floating_ip": {
		"active": Ready,
		"error":  Failed,
	},
	"load_balancer": {
		"active": Ready,
		// Stopped for non-payment. Terminal until the bill is settled, and not
		// a failure of the create that made it.
		"stopped_dunning":    Settled,
		"deleted":            Settled,
		"error_provisioning": Failed,
		"error_delete":       Failed,
	},
	// A power action is a JOB rather than a resource, and its vocabulary is its
	// own: it has no "ready", it has "the work finished". Kept in the same table
	// because the waiter's rule for an unknown value has to apply here too.
	"action": {
		"succeeded": Ready,
		"failed":    Failed,
	},
	"database": {
		"active":             Ready,
		"stopped_dunning":    Settled,
		"suspended":          Settled,
		"deleted":            Settled,
		"error_provisioning": Failed,
		"error_delete":       Failed,
	},
	// An ISO's state field is spelled `status`, which is the only thing that
	// makes it look different from the rest. A create answers before the
	// download has started, so this is polled exactly like a provisioning
	// resource.
	"iso": {
		"ready": Ready,
		"error": Failed,
	},
	// A build, like an action, is WORK rather than a resource. `canceled` is the
	// customer's own stop: terminal, and not a failure, so it must not raise a
	// *StateError -- a caller who cancelled their own build does not want it
	// reported back to them as a broken one.
	"build": {
		"succeeded": Ready,
		"canceled":  Settled,
		"failed":    Failed,
	},
	// A registration. `expired` and `redemption` are settled and are NOT
	// failures of the register that produced them: the name was really bought
	// and really lapsed later, which is a different conversation from a registry
	// that refused the order.
	"domain": {
		"active":     Ready,
		"expired":    Settled,
		"redemption": Settled,
		"failed":     Failed,
	},
}

// Classify answers what one state value means for one kind of resource.
func Classify(kind, state string) Outcome {
	if m, ok := resourceStates[kind]; ok {
		if o, ok := m[state]; ok {
			return o
		}
	}
	return Working
}

// Done reports whether polling can stop: anything that is not Working.
func Done(kind, state string) bool { return Classify(kind, state) != Working }
