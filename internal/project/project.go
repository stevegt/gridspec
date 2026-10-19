// Package project applies GridSpec acceptance policy on top of Grid objects
// and Git carrier refs.
package project

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ipfs/go-cid"

	"github.com/promisegrid/gridspec/internal/app"
	"github.com/promisegrid/gridspec/internal/artifact"
	"github.com/promisegrid/gridspec/internal/commitv3"
	"github.com/promisegrid/gridspec/internal/config"
	"github.com/promisegrid/gridspec/internal/conformanceevidence"
	"github.com/promisegrid/gridspec/internal/diag"
	"github.com/promisegrid/gridspec/internal/gitstore"
	"github.com/promisegrid/gridspec/internal/gridwire"
	"github.com/promisegrid/gridspec/internal/specsource"
	"github.com/promisegrid/gridspec/internal/specstate"
	"github.com/promisegrid/gridspec/internal/transaction"
)

const (
	projectConfig = "gridspec.project"
	profileConfig = "gridspec.profile"
	agentConfig   = "gridspec.agent"
	keyConfig     = "gridspec.signingKey"
)

type Project struct {
	Repository *gitstore.Repository
	Name       string
	Profile    string
	AgentID    cid.Cid
	Signer     ed25519.PrivateKey
}

type Snapshot struct {
	Commit  commitv3.Commit
	Carrier string
	Head    transaction.Event
	State   specstate.State
}

// Source is one verified, immutable compiler input. Commit is the signed
// accepted commit selecting the state, or (for a proposal) the accepted base
// commit which the proposed Event extends.
type Source struct {
	Identity specsource.Identity
	Commit   commitv3.Commit
	Event    transaction.Event
	State    specstate.State
	Carrier  string
}

type Proposal struct {
	Event   transaction.Event
	State   specstate.State
	Ref     string
	Carrier string
}

type Candidate struct {
	Artifact artifact.Artifact
	Ref      string
	Carrier  string
}

type EvidenceCandidate struct {
	Evidence conformanceevidence.Evidence
	Ref      string
	Carrier  string
}

type InitOptions struct {
	Dir     string
	Name    string
	Profile string
}

// Init creates a local signing identity and a first signed acceptance. Private
// key bytes live under .git and are never part of a carrier closure.
func Init(ctx context.Context, options InitOptions) (*Project, Snapshot, error) {
	if options.Name == "" || strings.ContainsAny(options.Name, "\x00\r\n") {
		return nil, Snapshot{}, fmt.Errorf("project name is required")
	}
	if options.Profile == "" {
		options.Profile = "default"
	}
	if _, _, err := config.Builtin(options.Name, options.Profile); err != nil {
		return nil, Snapshot{}, err
	}
	repository, err := gitstore.Open(ctx, options.Dir)
	if err != nil {
		return nil, Snapshot{}, err
	}
	configured, err := repository.Config(ctx, projectConfig)
	if err != nil {
		return nil, Snapshot{}, err
	}
	if configured != "" {
		if configured != options.Name {
			return nil, Snapshot{}, fmt.Errorf("repository is already GridSpec project %q", configured)
		}
		opened, err := Open(ctx, repository.Dir)
		if err != nil {
			return nil, Snapshot{}, err
		}
		snapshot, err := opened.Load(ctx)
		return opened, snapshot, err
	}

	signer, keyPath, err := createSigner(repository.GitDir)
	if err != nil {
		return nil, Snapshot{}, err
	}
	agentID, err := gridwire.RawCID(signer.Public().(ed25519.PublicKey))
	if err != nil {
		return nil, Snapshot{}, err
	}
	for key, value := range map[string]string{
		projectConfig: options.Name, profileConfig: options.Profile,
		agentConfig: agentID.String(), keyConfig: keyPath,
	} {
		if err := repository.SetConfig(ctx, key, value); err != nil {
			return nil, Snapshot{}, err
		}
	}
	current := &Project{Repository: repository, Name: options.Name, Profile: options.Profile, AgentID: agentID, Signer: signer}
	base, err := specstate.Build(ctx, repository, specstate.Empty(options.Name, options.Profile))
	if err != nil {
		return nil, Snapshot{}, err
	}
	bootstrap := []byte("gridspec 1\nmodule " + strconv.Quote(options.Name) + "\nprofile " + strconv.Quote(options.Profile) + "\n")
	event, state, err := transaction.Build(ctx, repository, transaction.BuildOptions{
		Base: base, Program: bootstrap, AgentID: agentID, Signer: signer,
	})
	if err != nil {
		return nil, Snapshot{}, err
	}
	commit, err := commitv3.Build(ctx, repository, commitv3.BuildOptions{
		Heads:    map[string]cid.Cid{"main": event.CID},
		Metadata: map[string]any{"project": options.Name, "profile": options.Profile, "operation": "init"},
		AgentID:  agentID, Signer: signer,
	})
	if err != nil {
		return nil, Snapshot{}, err
	}
	carrier, err := repository.Publish(ctx, commit.CID, commit.Closure(), current.AcceptedRef(), "", "GridSpec initial acceptance")
	if err != nil {
		return nil, Snapshot{}, err
	}
	return current, Snapshot{Commit: commit, Carrier: carrier, Head: event, State: state}, nil
}

// Open loads repository-local project identity without reading accepted state.
func Open(ctx context.Context, dir string) (*Project, error) {
	repository, err := gitstore.Open(ctx, dir)
	if err != nil {
		return nil, err
	}
	name, _ := repository.Config(ctx, projectConfig)
	profile, _ := repository.Config(ctx, profileConfig)
	agentText, _ := repository.Config(ctx, agentConfig)
	keyPath, _ := repository.Config(ctx, keyConfig)
	if name == "" || agentText == "" || keyPath == "" {
		return nil, fmt.Errorf("repository is not initialized; run gridspec init")
	}
	if profile == "" {
		profile = "default"
	}
	agentID, err := cid.Parse(agentText)
	if err != nil || agentID.Version() != 1 {
		return nil, fmt.Errorf("invalid configured GridSpec agent CID")
	}
	signer, err := loadSigner(keyPath)
	if err != nil {
		return nil, err
	}
	derived, _ := gridwire.RawCID(signer.Public().(ed25519.PublicKey))
	if !derived.Equals(agentID) {
		return nil, fmt.Errorf("configured signing key does not match agent CID")
	}
	return &Project{Repository: repository, Name: name, Profile: profile, AgentID: agentID, Signer: signer}, nil
}

func (project *Project) AcceptedRef() string {
	return "refs/gridspec/projects/p-" + hex.EncodeToString([]byte(project.Name)) + "/" + project.AgentID.String()
}

func (project *Project) ProposalRef(event cid.Cid) string {
	return "refs/gridspec/proposals/p-" + hex.EncodeToString([]byte(project.Name)) + "/" + event.String()
}

func (project *Project) CandidateRef(target string, artifactCID cid.Cid) string {
	return "refs/gridspec/candidates/p-" + hex.EncodeToString([]byte(project.Name)) + "/t-" + hex.EncodeToString([]byte(target)) + "/" + artifactCID.String()
}

// Load verifies the signed accepted commit, its complete timeline selection,
// the selected transaction, deterministic replay, and state closure.
func (project *Project) Load(ctx context.Context) (Snapshot, error) {
	if project == nil || project.Repository == nil {
		return Snapshot{}, fmt.Errorf("project is required")
	}
	carrier, err := project.Repository.ResolveRef(ctx, project.AcceptedRef())
	if err != nil || carrier == "" {
		return Snapshot{}, fmt.Errorf("accepted GridSpec ref is absent")
	}
	root, loaded, err := project.Repository.AddCarrier(ctx, carrier)
	if err != nil {
		return Snapshot{}, err
	}
	commit, err := project.verifyCommit(ctx, root)
	if err != nil {
		return Snapshot{}, fmt.Errorf("verify accepted Grid commit: %w", err)
	}
	headCID, ok := commit.Heads["main"]
	if !ok {
		return Snapshot{}, fmt.Errorf("accepted commit has no main timeline")
	}
	event, err := transaction.Verify(ctx, project.Repository, headCID)
	if err != nil {
		return Snapshot{}, err
	}
	state, err := specstate.Load(ctx, project.Repository, event.Result)
	if err != nil {
		return Snapshot{}, err
	}
	if state.Module != project.Name {
		return Snapshot{}, fmt.Errorf("accepted state module %q does not match project %q", state.Module, project.Name)
	}
	return Snapshot{Commit: commit, Carrier: loaded, Head: event, State: state}, nil
}

func (project *Project) verifyCommit(ctx context.Context, commitCID cid.Cid) (commitv3.Commit, error) {
	commit, err := commitv3.Verify(ctx, project.Repository, commitCID, func(agent cid.Cid, public ed25519.PublicKey) error {
		if !agent.Equals(project.AgentID) || !bytes.Equal(public, project.Signer.Public().(ed25519.PublicKey)) {
			return fmt.Errorf("accepted commit identity is not the configured project identity")
		}
		return nil
	})
	if err != nil {
		return commitv3.Commit{}, err
	}
	if commit.Verification.IdentityError != nil {
		return commitv3.Commit{}, commit.Verification.IdentityError
	}
	return commit, nil
}

func acceptedSource(snapshot Snapshot) Source {
	return Source{
		Identity: specsource.Identity{
			Kind: specsource.Accepted, AcceptedCommit: snapshot.Commit.CID,
			Event: snapshot.Head.CID, State: snapshot.State.CID,
		},
		Commit: snapshot.Commit, Event: snapshot.Head, State: snapshot.State, Carrier: snapshot.Carrier,
	}
}

// LoadSource resolves the signed accepted source by default, or an explicitly
// selected current proposal. Both paths verify complete CAS closure and replay
// before returning the materialized state.
func (project *Project) LoadSource(ctx context.Context, proposal string) (Source, error) {
	accepted, err := project.Load(ctx)
	if err != nil {
		return Source{}, err
	}
	if proposal == "" {
		return acceptedSource(accepted), nil
	}
	candidate, err := project.loadProposal(ctx, proposal, accepted)
	if err != nil {
		return Source{}, err
	}
	return Source{
		Identity: specsource.Identity{
			Kind: specsource.Proposal, AcceptedCommit: accepted.Commit.CID,
			Event: candidate.Event.CID, State: candidate.State.CID,
		},
		Commit: accepted.Commit, Event: candidate.Event, State: candidate.State, Carrier: candidate.Carrier,
	}, nil
}

// Propose replays exact transaction bytes against the accepted state and
// publishes an immutable proposal carrier. Tool/LLM output is unsigned unless
// authored is explicitly requested.
func (project *Project) Propose(ctx context.Context, program []byte, authored bool) (Proposal, error) {
	accepted, err := project.Load(ctx)
	if err != nil {
		return Proposal{}, err
	}
	return project.proposeSnapshot(ctx, accepted, program, authored, nil)
}

// ProposeFrom publishes an unsigned tool proposal against the exact accepted
// source read before a potentially long-running transformation. If acceptance
// advances meanwhile, the existing acceptance check rejects the stale result.
func (project *Project) ProposeFrom(ctx context.Context, source Source, program []byte, required []cid.Cid) (Proposal, error) {
	if err := source.Identity.Validate(); err != nil {
		return Proposal{}, err
	}
	if source.Identity.Kind != specsource.Accepted {
		return Proposal{}, fmt.Errorf("tool proposal base must be accepted specifications")
	}
	if !source.Identity.AcceptedCommit.Equals(source.Commit.CID) || !source.Identity.Event.Equals(source.Event.CID) || !source.Identity.State.Equals(source.State.CID) {
		return Proposal{}, fmt.Errorf("tool proposal source identity does not match its loaded objects")
	}
	return project.proposeSnapshot(ctx, Snapshot{
		Commit: source.Commit, Carrier: source.Carrier, Head: source.Event, State: source.State,
	}, program, false, required)
}

func (project *Project) proposeSnapshot(ctx context.Context, accepted Snapshot, program []byte, authored bool, required []cid.Cid) (Proposal, error) {
	options := transaction.BuildOptions{Previous: accepted.Head.CID, Base: accepted.State, Program: program}
	options.Required = append([]cid.Cid(nil), required...)
	if authored {
		options.AgentID, options.Signer = project.AgentID, project.Signer
	}
	event, state, err := transaction.Build(ctx, project.Repository, options)
	if err != nil {
		return Proposal{}, err
	}
	if err := validateState(state); err != nil {
		return Proposal{}, fmt.Errorf("vet proposal: %w", err)
	}
	ref := project.ProposalRef(event.CID)
	if existing, _ := project.Repository.ResolveRef(ctx, ref); existing != "" {
		root, _, err := project.Repository.AddCarrier(ctx, existing)
		if err != nil || !root.Equals(event.CID) {
			return Proposal{}, fmt.Errorf("existing proposal ref does not carry event %s", event.CID)
		}
		return Proposal{Event: event, State: state, Ref: ref, Carrier: existing}, nil
	}
	carrier, err := project.Repository.Publish(ctx, event.CID, event.Closure(), ref, "", "GridSpec transaction proposal")
	if err != nil {
		return Proposal{}, err
	}
	return Proposal{Event: event, State: state, Ref: ref, Carrier: carrier}, nil
}

// Accept selects a proposal as the new main head in a signed complete Grid
// Commit v3. Stale proposals cannot be accepted.
func (project *Project) Accept(ctx context.Context, proposal string) (Snapshot, error) {
	accepted, err := project.Load(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	candidate, err := project.loadProposal(ctx, proposal, accepted)
	if err != nil {
		return Snapshot{}, err
	}
	event, state := candidate.Event, candidate.State
	heads := make(map[string]cid.Cid, len(accepted.Commit.Heads))
	for timeline, head := range accepted.Commit.Heads {
		heads[timeline] = head
	}
	heads["main"] = event.CID
	commit, err := commitv3.Build(ctx, project.Repository, commitv3.BuildOptions{
		Predecessors: []cid.Cid{accepted.Commit.CID}, Heads: heads,
		Metadata: map[string]any{"project": project.Name, "profile": state.Profile, "operation": "accept", "transaction": event.CID.String()},
		AgentID:  project.AgentID, Signer: project.Signer,
	})
	if err != nil {
		return Snapshot{}, err
	}
	carrier, err := project.Repository.Publish(ctx, commit.CID, commit.Closure(), project.AcceptedRef(), accepted.Carrier, "GridSpec accepted transaction")
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Commit: commit, Carrier: carrier, Head: event, State: state}, nil
}

func (project *Project) loadProposal(ctx context.Context, proposal string, accepted Snapshot) (Proposal, error) {
	ref := proposal
	wanted := cid.Undef
	var err error
	if !strings.HasPrefix(proposal, "refs/") {
		wanted, err = cid.Parse(proposal)
		if err != nil {
			return Proposal{}, fmt.Errorf("proposal must be an event CID or GridSpec ref")
		}
		ref = project.ProposalRef(wanted)
	}
	root, carrier, err := project.Repository.AddCarrier(ctx, ref)
	if err != nil {
		return Proposal{}, err
	}
	if wanted.Defined() && !root.Equals(wanted) {
		return Proposal{}, fmt.Errorf("proposal ref ROOT does not match requested event")
	}
	event, err := transaction.Verify(ctx, project.Repository, root)
	if err != nil {
		return Proposal{}, fmt.Errorf("verify proposal: %w", err)
	}
	if !event.Previous.Equals(accepted.Head.CID) || !event.Base.Equals(accepted.State.CID) {
		return Proposal{}, fmt.Errorf("proposal is stale: it does not extend the accepted main timeline")
	}
	state, err := specstate.Load(ctx, project.Repository, event.Result)
	if err != nil {
		return Proposal{}, err
	}
	if state.Module != project.Name {
		return Proposal{}, fmt.Errorf("proposal state module %q does not match project %q", state.Module, project.Name)
	}
	if err := validateState(state); err != nil {
		return Proposal{}, fmt.Errorf("vet proposal before acceptance: %w", err)
	}
	return Proposal{Event: event, State: state, Ref: ref, Carrier: carrier}, nil
}

func validateState(state specstate.State) error {
	_, diagnostics, err := app.AnalyzeState(state, state.Profile, false)
	if err != nil {
		return err
	}
	count := 0
	var first diag.Diagnostic
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity != diag.SeverityError {
			continue
		}
		if count == 0 {
			first = diagnostic
		}
		count++
	}
	if count != 0 {
		return fmt.Errorf("%d error(s); first: %s", count, first.String())
	}
	return nil
}

// verifySource re-establishes source provenance from immutable CIDs without
// consulting the moving accepted ref. This lets a long-running compiler bind
// its output to the state it actually read even if acceptance advances.
func (project *Project) verifySource(ctx context.Context, identity specsource.Identity) (Source, error) {
	if project == nil || project.Repository == nil {
		return Source{}, fmt.Errorf("project is required")
	}
	if err := identity.Validate(); err != nil {
		return Source{}, err
	}
	commit, err := project.verifyCommit(ctx, identity.AcceptedCommit)
	if err != nil {
		return Source{}, fmt.Errorf("verify source accepted commit: %w", err)
	}
	headCID, ok := commit.Heads["main"]
	if !ok {
		return Source{}, fmt.Errorf("source accepted commit has no main timeline")
	}
	head, err := transaction.Verify(ctx, project.Repository, headCID)
	if err != nil {
		return Source{}, fmt.Errorf("verify source accepted event: %w", err)
	}
	event := head
	if identity.Kind == specsource.Accepted {
		if !identity.Event.Equals(head.CID) {
			return Source{}, fmt.Errorf("accepted source event is not selected by its accepted commit")
		}
	} else {
		event, err = transaction.Verify(ctx, project.Repository, identity.Event)
		if err != nil {
			return Source{}, fmt.Errorf("verify source proposal: %w", err)
		}
		if !event.Previous.Equals(head.CID) || !event.Base.Equals(head.Result) {
			return Source{}, fmt.Errorf("proposal source does not extend its accepted base commit")
		}
	}
	if !event.Result.Equals(identity.State) {
		return Source{}, fmt.Errorf("source state is not the selected event result")
	}
	state, err := specstate.Load(ctx, project.Repository, identity.State)
	if err != nil {
		return Source{}, fmt.Errorf("load source state: %w", err)
	}
	if state.Module != project.Name {
		return Source{}, fmt.Errorf("source state module %q does not match project %q", state.Module, project.Name)
	}
	if err := validateState(state); err != nil {
		return Source{}, fmt.Errorf("vet source state: %w", err)
	}
	return Source{Identity: identity, Commit: commit, Event: event, State: state}, nil
}

// PublishCandidate records deterministic compiler output and exact verification
// evidence without granting the artifact specification authority.
func (project *Project) PublishCandidate(ctx context.Context, source specsource.Identity, target, generator, outputDigest string, evidence []byte) (Candidate, error) {
	verified, err := project.verifySource(ctx, source)
	if err != nil {
		return Candidate{}, err
	}
	inputs := []cid.Cid{source.State}
	if source.IsProposal() {
		inputs = append(inputs, source.Event)
	}
	derived, err := artifact.Build(ctx, project.Repository, artifact.BuildOptions{
		AcceptedCommit: source.AcceptedCommit, Target: target, Generator: generator,
		Inputs: inputs, OutputDigest: outputDigest,
		VerificationEvidence: evidence,
	})
	if err != nil {
		return Candidate{}, err
	}
	closure := append(derived.Closure(), verified.Commit.Closure()...)
	closure = append(closure, verified.Event.Closure()...)
	ref := project.CandidateRef(target, derived.CID)
	if existing, _ := project.Repository.ResolveRef(ctx, ref); existing != "" {
		root, _, err := project.Repository.AddCarrier(ctx, existing)
		if err != nil || !root.Equals(derived.CID) {
			return Candidate{}, fmt.Errorf("existing candidate ref does not carry artifact %s", derived.CID)
		}
		return Candidate{Artifact: derived, Ref: ref, Carrier: existing}, nil
	}
	carrier, err := project.Repository.Publish(ctx, derived.CID, closure, ref, "", "GridSpec derived artifact candidate")
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{Artifact: derived, Ref: ref, Carrier: carrier}, nil
}

// PublishConformanceEvidence records exact bounded evidence without changing
// accepted specification state.
func (project *Project) PublishConformanceEvidence(ctx context.Context, source specsource.Identity, implementationDigest string, configuration, results, exclusions, diagnostics, report []byte) (EvidenceCandidate, error) {
	if source.Kind != specsource.Accepted {
		return EvidenceCandidate{}, fmt.Errorf("recorded conformance evidence requires accepted specifications")
	}
	accepted, err := project.verifySource(ctx, source)
	if err != nil {
		return EvidenceCandidate{}, err
	}
	evidence, err := conformanceevidence.Build(ctx, project.Repository, conformanceevidence.BuildOptions{
		AcceptedCommit: source.AcceptedCommit, ImplementationDigest: implementationDigest,
		CheckerConfiguration: configuration, EvidenceResults: results,
		Exclusions: exclusions, Diagnostics: diagnostics, Report: report,
	})
	if err != nil {
		return EvidenceCandidate{}, err
	}
	closure := append(evidence.Closure(), accepted.Commit.Closure()...)
	ref := project.CandidateRef("conformance", evidence.CID)
	if existing, _ := project.Repository.ResolveRef(ctx, ref); existing != "" {
		root, _, err := project.Repository.AddCarrier(ctx, existing)
		if err != nil || !root.Equals(evidence.CID) {
			return EvidenceCandidate{}, fmt.Errorf("existing evidence ref does not carry %s", evidence.CID)
		}
		return EvidenceCandidate{Evidence: evidence, Ref: ref, Carrier: existing}, nil
	}
	carrier, err := project.Repository.Publish(ctx, evidence.CID, closure, ref, "", "GridSpec conformance evidence")
	if err != nil {
		return EvidenceCandidate{}, err
	}
	return EvidenceCandidate{Evidence: evidence, Ref: ref, Carrier: carrier}, nil
}

func createSigner(gitDir string) (ed25519.PrivateKey, string, error) {
	directory := filepath.Join(gitDir, "gridspec", "keys")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, "", err
	}
	path := filepath.Join(directory, "signing-key.pem")
	if _, err := os.Stat(path); err == nil {
		signer, err := loadSigner(path)
		return signer, path, err
	} else if !os.IsNotExist(err) {
		return nil, "", err
	}
	_, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(signer)
	if err != nil {
		return nil, "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", err
	}
	writeErr := pem.Encode(file, &pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
	closeErr := file.Close()
	if writeErr != nil {
		return nil, "", writeErr
	}
	return signer, path, closeErr
}

func loadSigner(path string) (ed25519.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read GridSpec signing key: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("GridSpec signing key %s must not be group/world accessible", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(data)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("invalid GridSpec PKCS#8 key file")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	signer, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(signer) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("GridSpec signing key is not Ed25519")
	}
	return signer, nil
}
