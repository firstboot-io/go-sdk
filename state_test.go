package firstboot

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/firstboot-io/go-sdk/fbapi"
)

// The gate that keeps state.go honest.
//
// `resourceStates` is hand-written and the enum it describes is generated, so
// the two drift the moment the API adds a state. The drift is silent by design:
// an unknown value counts as still working, which is the SAFE default and
// therefore the one that hides the problem. A waiter would poll a finished
// server until its timeout and report a platform failure that did not happen.
//
// So the enum is the authority and this test is what makes it one. Regenerating
// `fbapi` after an API change either passes here or fails with the exact value
// nobody classified.

// generatedStates pairs each kind in resourceStates with the generated enum
// that is its authority. A kind with no entry here is unchecked, so the map is
// also the list of what this test can see.
var generatedStates = map[string][]string{
	"server": {
		string(fbapi.ServerBodyStateProvisioning), string(fbapi.ServerBodyStateRunning),
		string(fbapi.ServerBodyStateStopping), string(fbapi.ServerBodyStateStopped),
		string(fbapi.ServerBodyStateStarting), string(fbapi.ServerBodyStateRebooting),
		string(fbapi.ServerBodyStateResizing), string(fbapi.ServerBodyStateRebuilding),
		string(fbapi.ServerBodyStateReconfiguring), string(fbapi.ServerBodyStateRestoring),
		string(fbapi.ServerBodyStateSuspending), string(fbapi.ServerBodyStateSuspended),
		string(fbapi.ServerBodyStateAbuseSuspended), string(fbapi.ServerBodyStateDeleting),
		string(fbapi.ServerBodyStateDeleted), string(fbapi.ServerBodyStateErrorProvisioning),
		string(fbapi.ServerBodyStateErrorResize), string(fbapi.ServerBodyStateErrorDelete),
		string(fbapi.ServerBodyStateErrorRebuild), string(fbapi.ServerBodyStateErrorReconfigure),
		string(fbapi.ServerBodyStateErrorRestore),
	},
	"volume": {
		string(fbapi.VolumeBodyStateCreating), string(fbapi.VolumeBodyStateAvailable),
		string(fbapi.VolumeBodyStateAttaching), string(fbapi.VolumeBodyStateAttached),
		string(fbapi.VolumeBodyStateDetaching), string(fbapi.VolumeBodyStateResizing),
		string(fbapi.VolumeBodyStateDeleting), string(fbapi.VolumeBodyStateError),
	},
	"network": {
		string(fbapi.NetworkBodyStateCreating), string(fbapi.NetworkBodyStateActive),
		string(fbapi.NetworkBodyStateDeleting), string(fbapi.NetworkBodyStateError),
	},
	"floating_ip": {
		string(fbapi.FloatingIPBodyStateProvisioning), string(fbapi.FloatingIPBodyStateActive),
		string(fbapi.FloatingIPBodyStateDeleting), string(fbapi.FloatingIPBodyStateError),
	},
	"load_balancer": {
		string(fbapi.LoadBalancerBodyStateProvisioning), string(fbapi.LoadBalancerBodyStateActive),
		string(fbapi.LoadBalancerBodyStateStoppedDunning), string(fbapi.LoadBalancerBodyStateDeleting),
		string(fbapi.LoadBalancerBodyStateDeleted), string(fbapi.LoadBalancerBodyStateErrorProvisioning),
		string(fbapi.LoadBalancerBodyStateErrorDelete),
	},
	"database": {
		string(fbapi.DatabaseBodyStateProvisioning), string(fbapi.DatabaseBodyStateActive),
		string(fbapi.DatabaseBodyStateStoppedDunning), string(fbapi.DatabaseBodyStateSuspended),
		string(fbapi.DatabaseBodyStateResizing), string(fbapi.DatabaseBodyStateDeleting),
		string(fbapi.DatabaseBodyStateDeleted), string(fbapi.DatabaseBodyStateErrorProvisioning),
		string(fbapi.DatabaseBodyStateErrorDelete),
	},
	"iso": {
		string(fbapi.IsoBodyStatusPending), string(fbapi.IsoBodyStatusDownloading),
		string(fbapi.IsoBodyStatusReady), string(fbapi.IsoBodyStatusError),
	},
	"domain": {
		string(fbapi.DomainBodyStateRegistering), string(fbapi.DomainBodyStateTransferPending),
		string(fbapi.DomainBodyStateActive), string(fbapi.DomainBodyStateExpired),
		string(fbapi.DomainBodyStateRedemption), string(fbapi.DomainBodyStateFailed),
	},
	// The two that are WORK rather than resources. Their vocabulary is a job's,
	// and the rule for an unknown value has to reach them too.
	"action": {
		string(fbapi.ActionBodyStateQueued), string(fbapi.ActionBodyStateRunning),
		string(fbapi.ActionBodyStateSucceeded), string(fbapi.ActionBodyStateFailed),
	},
	"build": {
		string(fbapi.BuildBodyStateQueued), string(fbapi.BuildBodyStateBuilding),
		string(fbapi.BuildBodyStateSucceeded), string(fbapi.BuildBodyStateFailed),
		string(fbapi.BuildBodyStateCanceled),
	},
}

// TestEveryGeneratedStateIsClassified fails when the API knows a state this
// package does not.
//
// A missing entry is not always a bug -- a transient state SHOULD be absent,
// because absent means "keep polling". What the test actually enforces is that
// somebody LOOKED: every value has to be either classified in resourceStates or
// named in transientStates below with the reason it is transient.
func TestEveryGeneratedStateIsClassified(t *testing.T) {
	for kind, values := range generatedStates {
		table, ok := resourceStates[kind]
		if !ok {
			t.Fatalf("%s: generatedStates names a kind resourceStates does not have", kind)
		}
		var unclassified []string
		for _, v := range values {
			if _, decided := table[v]; decided {
				continue
			}
			if _, transient := transientStates[kind+"."+v]; transient {
				continue
			}
			unclassified = append(unclassified, v)
		}
		sort.Strings(unclassified)
		if len(unclassified) > 0 {
			t.Errorf("%s: the API has states this package has not classified: %s\n"+
				"  Add each to resourceStates[%q] (terminal) or to transientStates (still working).",
				kind, strings.Join(unclassified, ", "), kind)
		}
	}
}

// transientStates names the values that are deliberately NOT in resourceStates,
// because they mean the work is still running. Listing them is the difference
// between "we decided this is transient" and "we forgot".
var transientStates = map[string]string{
	"server.provisioning":  "the create is running",
	"server.starting":      "a power action is running",
	"server.stopping":      "a power action is running",
	"server.rebooting":     "a power action is running",
	"server.resizing":      "a resize is running",
	"server.rebuilding":    "a rebuild is running",
	"server.reconfiguring": "a network or firewall change is being applied",
	"server.restoring":     "a backup or snapshot restore is running",
	"server.suspending":    "the freeze is being applied",
	"server.deleting":      "the delete is running",

	"volume.creating":  "the create is running",
	"volume.attaching": "an attach is running",
	"volume.detaching": "a detach is running",
	"volume.resizing":  "a grow is running",
	"volume.deleting":  "the delete is running",

	"network.creating": "the create is running",
	"network.deleting": "the delete is running",

	"floating_ip.provisioning": "the address is being allocated",
	"floating_ip.deleting":     "the release is running",

	"load_balancer.provisioning": "the create is running",
	"load_balancer.deleting":     "the delete is running",

	"database.provisioning": "the create is running",
	"database.resizing":     "a plan change is being applied",
	"database.deleting":     "the delete is running",

	"iso.pending":     "the download has not started yet",
	"iso.downloading": "the download is running",

	// A registry's answer, not this platform's. Both can sit here for a long
	// time and neither is a reason to stop polling.
	"domain.registering":      "the registry has not answered the order yet",
	"domain.transfer_pending": "the losing registrar's window has not closed",

	"action.queued":  "the job has not started",
	"action.running": "the job is running",

	"build.queued":   "the build has not started",
	"build.building": "the build is running",
}

// TestClassifyTreatsTheUnknownAsWorking pins the asymmetry the whole design
// rests on. It is the one behaviour that must never be 'fixed' into symmetry.
func TestClassifyTreatsTheUnknownAsWorking(t *testing.T) {
	if got := Classify("server", "a_state_from_a_newer_api"); got != Working {
		t.Fatalf("an unknown state must count as still working, got %v", got)
	}
	if got := Classify("a_resource_this_client_does_not_know", "running"); got != Working {
		t.Fatalf("an unknown resource kind must count as still working, got %v", got)
	}
	if Done("server", "a_state_from_a_newer_api") {
		t.Fatal("Done must not stop on a state it cannot classify")
	}
}

// TestFailureStatesProduceAnError guards the other half: a settled failure has
// to be distinguishable from a settled success, or a waiter returns a broken
// server with a nil error.
func TestFailureStatesProduceAnError(t *testing.T) {
	for kind, table := range resourceStates {
		var ready, failed int
		for _, outcome := range table {
			switch outcome {
			case Ready:
				ready++
			case Failed:
				failed++
			}
		}
		if ready == 0 {
			t.Errorf("%s: no state is classified Ready, so a create can never finish successfully", kind)
		}
		if failed == 0 {
			t.Errorf("%s: no state is classified Failed, so a failed create waits until its timeout", kind)
		}
	}
}

func TestLeadingCode(t *testing.T) {
	for _, c := range []struct{ detail, want string }{
		{"NO_CAPACITY_IN_REGION", "NO_CAPACITY_IN_REGION"},
		{"USER_DATA_NOT_SUPPORTED: user-data is only accepted with OS images", "USER_DATA_NOT_SUPPORTED"},
		{"IDEMPOTENCY_KEY_REUSED", "IDEMPOTENCY_KEY_REUSED"},
		// huma's own validation messages carry no code, and inventing one from
		// the first word would give callers something to branch on that the API
		// never promised.
		{"expected required property name to be present", ""},
		{"", ""},
		{"lowercase_thing", ""},
	} {
		if got := leadingCode(c.detail); got != c.want {
			t.Errorf("leadingCode(%q) = %q, want %q", c.detail, got, c.want)
		}
	}
}

func TestAPIErrorMatchesBySentinel(t *testing.T) {
	err := error(&APIError{Status: 503, Code: "NO_CAPACITY_IN_REGION", Detail: "NO_CAPACITY_IN_REGION"})
	if !errors.Is(err, ErrNoCapacity) {
		t.Fatal("a real error must match its sentinel")
	}
	if errors.Is(err, ErrPlanNotOffered) {
		t.Fatal("it must not match a different sentinel")
	}
	// The two used to be one 503, and telling them apart is the point: waiting
	// helps for one and can never help for the other.
	if fmt.Sprint(ErrNoCapacity) == fmt.Sprint(ErrPlanNotOffered) {
		t.Fatal("the two sentinels render identically")
	}
}
