package project

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ipfs/go-cid"

	"github.com/promisegrid/gridspec/internal/artifact"
	"github.com/promisegrid/gridspec/internal/conformanceevidence"
	"github.com/promisegrid/gridspec/internal/gitstore"
	"github.com/promisegrid/gridspec/internal/gridwire"
)

func TestInitProposeAcceptAndReload(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", output, err)
	}
	current, initial, err := Init(ctx, InitOptions{Dir: dir, Name: "example.test/system", Profile: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if initial.Commit.Verification.SignatureError != nil || initial.Head.Proof == nil {
		t.Fatalf("initial acceptance is not authored and signed: %#v", initial)
	}
	program := []byte("gridspec 1\nput graph overview.graph { kind = \"descriptive\", status = \"active\", directed = true, strict = true, attrs = {} }\nput document overview { path = \"docs/overview.md\", content = \"\", sha256 = \"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\", anchors = {}, links = null }\n")
	proposal, err := current.Propose(ctx, program, false)
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Event.Proof != nil {
		t.Fatal("tool proposal unexpectedly has an authorship signature")
	}
	accepted, err := current.Accept(ctx, proposal.Event.CID.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := accepted.State.Entities["document\x00overview"]; !ok {
		t.Fatal("accepted state omits proposed entity")
	}
	selected, err := current.LoadSource(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := current.PublishCandidate(ctx, selected.Identity, "go-build", "static/test", "sha256:abc", []byte("go test passed"))
	if err != nil {
		t.Fatal(err)
	}
	candidateStore, err := gitstore.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	candidateRoot, _, err := candidateStore.AddCarrier(ctx, candidate.Ref)
	if err != nil || !candidateRoot.Equals(candidate.Artifact.CID) {
		t.Fatalf("candidate root=%s err=%v", candidateRoot, err)
	}
	if _, err := artifact.Verify(ctx, candidateStore, candidateRoot); err != nil {
		t.Fatal(err)
	}
	evidence, err := current.PublishConformanceEvidence(ctx, selected.Identity, "sha256:implementation", []byte("config"), []byte("results"), []byte("[]"), []byte("[]"), []byte("report"))
	if err != nil {
		t.Fatal(err)
	}
	evidenceStore, _ := gitstore.Open(ctx, dir)
	evidenceRoot, _, err := evidenceStore.AddCarrier(ctx, evidence.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conformanceevidence.Verify(ctx, evidenceStore, evidenceRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := current.Accept(ctx, proposal.Event.CID.String()); err == nil {
		t.Fatal("stale proposal was accepted twice")
	}

	reopened, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := reopened.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Commit.CID.Equals(accepted.Commit.CID) || !reloaded.State.CID.Equals(accepted.State.CID) {
		t.Fatalf("reloaded acceptance differs: %s %s", reloaded.Commit.CID, reloaded.State.CID)
	}
	keyPath, _ := reopened.Repository.Config(ctx, keyConfig)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private key permissions = %v", info.Mode().Perm())
	}
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	hash := exec.Command("git", "hash-object", "--stdin")
	hash.Dir, hash.Stdin = dir, bytes.NewReader(keyBytes)
	objectID, err := hash.Output()
	if err != nil {
		t.Fatal(err)
	}
	probe := exec.Command("git", "cat-file", "-e", strings.TrimSpace(string(objectID)))
	probe.Dir = dir
	if err := probe.Run(); err == nil {
		t.Fatal("private signing key was written to the Git object database")
	}
}

func TestProposeFromPinsSourceAndCarriesRequiredAudit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", output, err)
	}
	current, _, err := Init(ctx, InitOptions{Dir: dir, Name: "example.test/pinned", Profile: "default"})
	if err != nil {
		t.Fatal(err)
	}
	source, err := current.LoadSource(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	audit := []byte(`{"schema":"test-audit/v1"}`)
	auditCID, err := gridwire.RawCID(audit)
	if err != nil {
		t.Fatal(err)
	}
	if err := current.Repository.Put(ctx, auditCID, audit); err != nil {
		t.Fatal(err)
	}
	program := []byte("gridspec 1\nput graph graph.test { title = \"Pinned proposal\", kind = \"decision\", status = \"active\", directed = true, attrs = {} }\n")
	proposal, err := current.ProposeFrom(ctx, source, program, []cid.Cid{auditCID})
	if err != nil {
		t.Fatal(err)
	}
	if !proposal.Event.Base.Equals(source.State.CID) || !proposal.Event.Previous.Equals(source.Event.CID) || !hasCID(proposal.Event.Required, auditCID) {
		t.Fatalf("proposal did not retain pinned source and audit: %#v", proposal.Event)
	}
}

func TestInvalidProposalIsNotPublished(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", output, err)
	}
	current, _, err := Init(ctx, InitOptions{Dir: dir, Name: "example.test/invalid", Profile: "default"})
	if err != nil {
		t.Fatal(err)
	}
	program := []byte("gridspec 1\nput document broken { path = \"docs/broken.md\" }\n")
	if _, err := current.Propose(ctx, program, false); err == nil || !strings.Contains(err.Error(), "content does not match") {
		t.Fatalf("invalid proposal error = %v", err)
	}
}

func TestProposalSourceAndPinnedArtifactProvenance(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", output, err)
	}
	current, _, err := Init(ctx, InitOptions{Dir: dir, Name: "example.test/source", Profile: "default"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := current.Propose(ctx, []byte("gridspec 1\nput graph overview { kind = \"descriptive\", status = \"active\", directed = true, strict = true, attrs = {} }\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := current.Accept(ctx, first.Event.CID.String()); err != nil {
		t.Fatal(err)
	}
	pinned, err := current.LoadSource(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := current.Propose(ctx, []byte("gridspec 1\nassert exists graph overview\n"), false)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := current.LoadSource(ctx, second.Event.CID.String())
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Identity.IsProposal() || !preview.Identity.State.Equals(second.State.CID) {
		t.Fatalf("unexpected proposal source: %#v", preview.Identity)
	}
	previewArtifact, err := current.PublishCandidate(ctx, preview.Identity, "go-build-preview", "static/test", "sha256:preview", []byte("preview tests passed"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasCID(previewArtifact.Artifact.Inputs, preview.Identity.Event) || !hasCID(previewArtifact.Artifact.Inputs, preview.Identity.State) {
		t.Fatalf("preview inputs do not identify event and state: %v", previewArtifact.Artifact.Inputs)
	}
	if _, err := current.PublishConformanceEvidence(ctx, preview.Identity, "sha256:implementation", []byte("config"), []byte("results"), []byte("[]"), []byte("[]"), []byte("report")); err == nil {
		t.Fatal("proposal conformance evidence was published")
	}
	if _, err := current.Accept(ctx, second.Event.CID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := current.LoadSource(ctx, second.Event.CID.String()); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("accepted proposal remained loadable as a current preview: %v", err)
	}
	oldArtifact, err := current.PublishCandidate(ctx, pinned.Identity, "go-build", "static/test", "sha256:pinned", []byte("tests passed"))
	if err != nil {
		t.Fatal(err)
	}
	if !oldArtifact.Artifact.AcceptedCommit.Equals(pinned.Identity.AcceptedCommit) {
		t.Fatalf("artifact commit = %s, want pinned %s", oldArtifact.Artifact.AcceptedCommit, pinned.Identity.AcceptedCommit)
	}
	latest, err := current.LoadSource(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if oldArtifact.Artifact.AcceptedCommit.Equals(latest.Identity.AcceptedCommit) {
		t.Fatal("old artifact was relabeled with the newer accepted commit")
	}
}

func hasCID(values []cid.Cid, wanted cid.Cid) bool {
	for _, value := range values {
		if value.Equals(wanted) {
			return true
		}
	}
	return false
}
