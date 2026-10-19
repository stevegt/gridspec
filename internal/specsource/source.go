// Package specsource identifies one immutable, CAS-backed GridSpec compiler
// input. Authority selection is verified by project; consumers use these CIDs
// to reload the exact state instead of trusting a loose in-memory value.
package specsource

import (
	"fmt"

	"github.com/ipfs/go-cid"
)

type Kind string

const (
	Accepted Kind = "accepted"
	Proposal Kind = "proposal"
)

// Identity pins the complete provenance needed by compiler outputs. For a
// proposal, AcceptedCommit is the signed commit whose main head the proposal
// extends; Event and State identify the proposed transition and result.
type Identity struct {
	Kind           Kind
	AcceptedCommit cid.Cid
	Event          cid.Cid
	State          cid.Cid
}

func (identity Identity) Validate() error {
	if identity.Kind != Accepted && identity.Kind != Proposal {
		return fmt.Errorf("source kind must be accepted or proposal")
	}
	for name, value := range map[string]cid.Cid{
		"accepted commit": identity.AcceptedCommit,
		"event":           identity.Event,
		"state":           identity.State,
	} {
		if !value.Defined() || value.Version() != 1 {
			return fmt.Errorf("source %s must be a CIDv1", name)
		}
	}
	return nil
}

func (identity Identity) IsProposal() bool { return identity.Kind == Proposal }
