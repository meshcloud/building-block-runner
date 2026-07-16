package tf

// Shared, hermetic scenario-test fixtures.
// These replace the scenario suites' dependency on a real `https://github.com/meshcloud/
// meshstack-hub.git` clone with an on-disk git repository, and give every later test file
// one builder for fixture Runs and one seam for genuinely encrypting/decrypting
// sensitive inputs instead of asserting ciphertext shapes.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	meshcrypto "github.com/meshcloud/building-block-runner/internal/crypto"
	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
)

// --- local git repository fixtures -----------------------------------------------------------

// localGitRepo is a real, on-disk git repository standing in for a remote: go-git's
// CloneOptions.URL accepts a filesystem path, so GitSource/Git clone it exactly as they would a
// remote over the network, with zero network I/O and no external test-runtime dependency.
type localGitRepo struct {
	// Path is the filesystem path to use as a GitSource url / TerraformImplementation.RepositoryUrl.
	Path string
	// Head is the initial commit's hash on the repo's default branch ("master", go-git's PlainInit
	// default), useful for tests that pin a refName to a raw commit hash.
	Head plumbing.Hash
}

type localGitRepoOptions struct {
	branches map[string]map[string]string
	tags     []string
}

type localGitRepoOption func(*localGitRepoOptions)

// withGitBranch adds an extra branch on top of the initial commit, optionally committing extra
// files onto it (nil/empty leaves the branch pointing at the initial commit). go-git's PlainClone
// fetches every branch into refs/remotes/origin/<name>, matching git.go's checkoutRef resolution
// order (branch, then tag, then raw commit hash).
func withGitBranch(name string, files map[string]string) localGitRepoOption {
	return func(o *localGitRepoOptions) {
		if o.branches == nil {
			o.branches = make(map[string]map[string]string)
		}
		o.branches[name] = files
	}
}

// withGitTag tags the initial commit on the repo's default branch.
func withGitTag(name string) localGitRepoOption {
	return func(o *localGitRepoOptions) {
		o.tags = append(o.tags, name)
	}
}

// makeLocalGitRepo creates a git repository (go-git PlainInit) under t.TempDir(), commits files as
// the initial commit, then applies opts (extra branches/tags). Fails the test on any git error —
// these are fixture-construction failures, not assertions under test.
//
// Construction is retried a few times on a fresh directory: go-git's on-disk operations
// (Commit/Checkout/CreateTag) occasionally fail transiently ("reference not found") when the full
// package suite runs them under heavy concurrent filesystem load. A fixture that cannot be built is
// setup noise, not a behavior under test, so a bounded rebuild keeps the suite deterministic without
// masking any product defect.
func makeLocalGitRepo(t *testing.T, files map[string]string, opts ...localGitRepoOption) *localGitRepo {
	t.Helper()

	options := localGitRepoOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	const attempts = 4
	var lastErr error
	for range attempts {
		repo, err := tryBuildLocalGitRepo(t.TempDir(), files, options)
		if err == nil {
			return repo
		}
		lastErr = err
	}
	t.Fatalf("makeLocalGitRepo: giving up after %d attempts: %v", attempts, lastErr)
	return nil
}

// tryBuildLocalGitRepo performs one construction attempt in dir, returning an error (rather than
// failing the test) so makeLocalGitRepo can retry a transient go-git failure on a fresh directory.
func tryBuildLocalGitRepo(dir string, files map[string]string, options localGitRepoOptions) (*localGitRepo, error) {
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		return nil, fmt.Errorf("PlainInit: %w", err)
	}

	head, err := commitGitFiles(repo, dir, files, "initial commit")
	if err != nil {
		return nil, err
	}

	w, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("Worktree: %w", err)
	}

	for name, branchFiles := range options.branches {
		branchRef := plumbing.NewBranchReferenceName(name)
		// Create the branch ref at the initial commit directly through the storer rather than via
		// Worktree.Checkout{Create:true}, which is the operation most prone to the transient
		// "reference not found" under load; the storer write is deterministic and enough for branches
		// that carry no extra commits.
		if err := repo.Storer.SetReference(plumbing.NewHashReference(branchRef, head)); err != nil {
			return nil, fmt.Errorf("create branch %q: %w", name, err)
		}
		if len(branchFiles) > 0 {
			if err := w.Checkout(&git.CheckoutOptions{Branch: branchRef, Force: true}); err != nil {
				return nil, fmt.Errorf("checkout branch %q: %w", name, err)
			}
			if _, err := commitGitFiles(repo, dir, branchFiles, fmt.Sprintf("commit on %s", name)); err != nil {
				return nil, err
			}
			// back to the default branch so the next branch option also starts from the initial commit.
			if err := w.Checkout(&git.CheckoutOptions{Branch: plumbing.Master, Force: true}); err != nil {
				return nil, fmt.Errorf("checkout back to default branch: %w", err)
			}
		}
	}

	for _, name := range options.tags {
		if _, err := repo.CreateTag(name, head, nil); err != nil {
			return nil, fmt.Errorf("create tag %q: %w", name, err)
		}
	}

	return &localGitRepo{Path: dir, Head: head}, nil
}

// commitGitFiles writes files (keyed by path relative to dir) into dir, stages and commits them,
// and returns the new commit's hash. AllowEmptyCommits covers the (rare, still valid) case of a
// branch/tag fixture that intentionally carries no file changes of its own.
func commitGitFiles(repo *git.Repository, dir string, files map[string]string, message string) (plumbing.Hash, error) {
	for relPath, content := range files {
		full := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return plumbing.ZeroHash, fmt.Errorf("MkdirAll %q: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			return plumbing.ZeroHash, fmt.Errorf("WriteFile %q: %w", full, err)
		}
	}

	w, err := repo.Worktree()
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("Worktree: %w", err)
	}
	if _, err := w.Add("."); err != nil {
		return plumbing.ZeroHash, fmt.Errorf("Add: %w", err)
	}

	commit, err := w.Commit(message, &git.CommitOptions{
		AllowEmptyCommits: true,
		Author:            &object.Signature{Name: "fixtures_test", Email: "fixtures-test@meshcloud.io"},
	})
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("Commit: %w", err)
	}

	return commit, nil
}

// --- Run fixture builder ------------------------------------------------------------

// runDetailsFixture is the mutable state runDetailsOptions apply to; runDetailsDTO/
// runDetailsFetchCall seed it with the defaults the former mock*FetchCall helpers hardwired
// (APPLY, no artifact, the shared mock run token) so existing call sites port over unchanged.
type runDetailsFixture struct {
	behavior                   string
	repoUrl                    string
	repoPath                   string
	refName                    *string
	sshPrivateKey              *string
	knownHost                  *meshapi.KnownHostDTO
	runToken                   string
	async                      bool
	useMeshHttpBackendFallback bool
	preRunScript               *string
	planArtifactHref           string
	inputs                     []meshapi.BuildingBlockInputSpecDTO
}

type runDetailsOption func(*runDetailsFixture)

func withBehavior(behavior string) runDetailsOption {
	return func(f *runDetailsFixture) { f.behavior = behavior }
}

// withRepo sets the git source: url is a remote URL or a localGitRepo.Path; path is the
// repository-relative subdirectory containing the terraform sources (mirrors
// TerraformImplementation.RepositoryPath — always set, "" for repo-root, never nil, matching prior
// behavior).
func withRepo(url, path string) runDetailsOption {
	return func(f *runDetailsFixture) { f.repoUrl = url; f.repoPath = path }
}

func withRefName(ref string) runDetailsOption {
	return func(f *runDetailsFixture) { f.refName = &ref }
}

func withSshAuth(privateKeyPem string, knownHost *meshapi.KnownHostDTO) runDetailsOption {
	return func(f *runDetailsFixture) { f.sshPrivateKey = &privateKeyPem; f.knownHost = knownHost }
}

func withRunToken(token string) runDetailsOption {
	return func(f *runDetailsFixture) { f.runToken = token }
}

func withAsync() runDetailsOption {
	return func(f *runDetailsFixture) { f.async = true }
}

func withMeshHttpBackendFallback() runDetailsOption {
	return func(f *runDetailsFixture) { f.useMeshHttpBackendFallback = true }
}

func withPreRunScript(script string) runDetailsOption {
	return func(f *runDetailsFixture) { f.preRunScript = &script }
}

// withPlanArtifact sets the runner-facing _links.planArtifact.href that signals an APPLY run must
// download and apply a predecessor DETECT run's saved plan instead of a plain apply.
func withPlanArtifact(href string) runDetailsOption {
	return func(f *runDetailsFixture) { f.planArtifactHref = href }
}

// withInputs appends building-block inputs; see buildingBlockInput/sensitiveInput/envInput below
// for constructing entries (incl. sensitive STRING/FILE values encrypted via encryptForTest).
func withInputs(inputs ...meshapi.BuildingBlockInputSpecDTO) runDetailsOption {
	return func(f *runDetailsFixture) { f.inputs = append(f.inputs, inputs...) }
}

// runDetailsDTO builds a fixture Run shaped like a real meshfed API response
// (RunSpecDTO/TerraformImplementation/BuildingBlockSpecDTO), configured via opts. The zero value
// (no opts) reproduces the minimal APPLY-without-artifact run the former mockValidRunDetailsFetchCall
// built.
func runDetailsDTO(opts ...runDetailsOption) *meshapi.Run {
	f := runDetailsFixture{
		behavior: APPLY.str(),
		runToken: "test-mock-run-token-12345",
	}
	for _, opt := range opts {
		opt(&f)
	}

	implDTO := meshapi.TerraformImplementation{
		TerraformVersion:           DEFAULT_TF_VER,
		RepositoryUrl:              f.repoUrl,
		RepositoryPath:             p(f.repoPath),
		RefName:                    f.refName,
		SshPrivateKey:              f.sshPrivateKey,
		KnownHost:                  f.knownHost,
		Async:                      f.async,
		UseMeshHttpBackendFallback: f.useMeshHttpBackendFallback,
		PreRunScript:               f.preRunScript,
	}
	// A marshal failure here would mean implDTO itself is unrepresentable, which cannot happen for
	// this concrete, plain-old-data struct; discarding the error mirrors the former fixture code.
	implJSON, _ := json.Marshal(implDTO)

	inputs := f.inputs
	if inputs == nil {
		inputs = make([]meshapi.BuildingBlockInputSpecDTO, 0)
	}

	dto := &meshapi.Run{
		ApiVersion: "v1",
		Kind:       "MeshBuildingBlockRun",
		Metadata:   meshapi.RunMetaDTO{Uuid: "run-uuid"},
		Spec: meshapi.RunSpecDTO{
			RunNumber: 1,
			Behavior:  f.behavior,
			RunToken:  f.runToken,
			BuildingBlock: meshapi.BuildingBlockSpecDTO{
				Uuid: "block-uuid",
				Spec: meshapi.BuildingBlockDetailsSpecDTO{
					DisplayName: "Test-BuildingBlock",
					Inputs:      inputs,
				},
			},
			Definition: meshapi.DefinitionSpecDTO{
				Uuid: "definition-uuid",
				Spec: meshapi.DefinitionDetailsSpecDTO{
					Version:        1,
					Implementation: implJSON,
				},
			},
		},
	}

	if f.planArtifactHref != "" {
		dto.Links.PlanArtifact = meshapi.LinkDTO{Href: f.planArtifactHref}
	}

	return dto
}

// runDetailsFetchCall wraps runDetailsDTO into a fake-transport "get run" handler
// (WorkerTestSuite.calls.fetch / an equivalent single-run fixture), replaying the same
// status/headers/media-type shape the real meshfed API returns.
func runDetailsFetchCall(opts ...runDetailsOption) func(_ *http.Request) *http.Response {
	dto := runDetailsDTO(opts...)

	return func(_ *http.Request) *http.Response {
		body, _ := json.Marshal(dto)
		header := make(http.Header)
		header.Add("Content-Type", meshapi.BlockRunMediaTypeV1)

		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBuffer(body)),
			Header:     header,
		}
	}
}

// buildingBlockInput builds one BuildingBlockInputSpecDTO for use with withInputs; see
// sensitiveInput/envInput for the isSensitive/isEnvironment flags.
func buildingBlockInput(key string, value any, dataType string, opts ...buildingBlockInputOption) meshapi.BuildingBlockInputSpecDTO {
	input := meshapi.BuildingBlockInputSpecDTO{Key: key, Value: value, Type: dataType}
	for _, opt := range opts {
		opt(&input)
	}

	return input
}

type buildingBlockInputOption func(*meshapi.BuildingBlockInputSpecDTO)

// sensitiveInput marks the input as encrypted (the claim-boundary decryptor decrypts CODE/STRING/
// FILE types — see encryptForTest to build a genuine ciphertext value for it).
func sensitiveInput() buildingBlockInputOption {
	return func(i *meshapi.BuildingBlockInputSpecDTO) { i.IsSensitive = true }
}

// envInput marks the input to be exposed via the tf process environment rather than a tfvars file.
func envInput() buildingBlockInputOption {
	return func(i *meshapi.BuildingBlockInputSpecDTO) { i.Env = true }
}

// --- encrypted-input helpers ------------------------------------------------------------------

// testCrypto loads the repo's checked-in test key pair (internal/resources/test.pem +
// test.key — the same material internal/crypto/meshcertbasedcrypto_test.go proves
// round-trips) so scenario tests can encrypt fixture input values and exercise the real decrypt
// path end to end, instead of asserting against ciphertext-shaped strings.
func testCrypto(t *testing.T) *meshcrypto.MeshCertBasedCrypto {
	t.Helper()

	pubKey, err := os.ReadFile("../resources/test.pem")
	if err != nil {
		t.Fatalf("testCrypto: reading test.pem: %v", err)
	}

	crypto, pubKeyErr, privateKeyErr := meshcrypto.NewCertBasedCrypto("../resources/test.key", pubKey)
	if pubKeyErr != nil || privateKeyErr != nil {
		t.Fatalf("testCrypto: NewCertBasedCrypto: pubKeyErr=%v privateKeyErr=%v", pubKeyErr, privateKeyErr)
	}

	return crypto
}

// encryptForTest encrypts plaintext with crypto, failing the test (rather than returning an error)
// since a fixture that cannot encrypt its own input is a broken test, not a case under test.
func encryptForTest(t *testing.T, crypto *meshcrypto.MeshCertBasedCrypto, plaintext string) string {
	t.Helper()

	ciphertext, err := crypto.EncryptMeshCertBased(plaintext)
	if err != nil {
		t.Fatalf("encryptForTest: %v", err)
	}

	return ciphertext
}

// --- shared worker-suite construction -----------------------------------------------------------

// newScenarioRunApiClient builds a *RunApiClient wired to transport instead of a real network
// connection, mirroring production wiring (NewRunApi: same runApiAuth precedence, same
// meshapi.Client construction) so the worker-execution (WorkerTestSuite) and single-run
// (cmd/tf's executeSingleRun) scenario suites share one construction path. The run-scoped
// runToken (if any) is carried on auth, not mutated post-construction (the deleted SetRunToken
// protocol).
func newScenarioRunApiClient(rid string, auth *runApiAuth, transport http.RoundTripper) *RunApiClient {
	hc := &http.Client{Transport: transport}

	return &RunApiClient{
		rid:        rid,
		baseURL:    "http://localhost",
		auth:       auth,
		client:     meshapi.NewClientWithHTTP("http://localhost", rid, auth, hc),
		httpClient: hc,
	}
}

// --- fixture self-tests --------------------------------------------------------------------
//
// These prove the shared fixtures themselves are correct, independent of any use-case scenario
// that will consume them in later test files — the option constructors below have no
// other caller yet.

// Test_MakeLocalGitRepo proves the local-repo fixture behaves like a real remote: a plain clone
// fetches the default branch, every extra branch (into refs/remotes/origin/<name>), and every tag,
// with no network access.
func Test_MakeLocalGitRepo(t *testing.T) {
	repo := makeLocalGitRepo(t,
		map[string]string{"main.tf": "# root\n"},
		withGitBranch("feature", map[string]string{"feature.tf": "# feature\n"}),
		withGitBranch("empty-branch", nil),
		withGitTag("v1.0.0"),
	)

	dst := t.TempDir()
	cloned, err := git.PlainClone(dst, false, &git.CloneOptions{URL: repo.Path})
	if err != nil {
		t.Fatalf("PlainClone: %v", err)
	}

	if _, err := cloned.Reference(plumbing.NewRemoteReferenceName("origin", "feature"), true); err != nil {
		t.Errorf("expected branch 'feature' to be fetched: %v", err)
	}
	if _, err := cloned.Reference(plumbing.NewRemoteReferenceName("origin", "empty-branch"), true); err != nil {
		t.Errorf("expected branch 'empty-branch' to be fetched: %v", err)
	}
	if _, err := cloned.Reference(plumbing.NewTagReferenceName("v1.0.0"), true); err != nil {
		t.Errorf("expected tag 'v1.0.0' to be fetched: %v", err)
	}
	if _, err := cloned.CommitObject(repo.Head); err != nil {
		t.Errorf("expected Head commit %s to exist: %v", repo.Head, err)
	}
}

// Test_Run_Options exercises every runDetailsOption and buildingBlockInputOption,
// proving each mutates the fixture as documented, and proves the encrypted-input helpers
// (testCrypto/encryptForTest) round-trip genuinely (not just ciphertext-shaped).
func Test_Run_Options(t *testing.T) {
	crypto := testCrypto(t)
	ciphertext := encryptForTest(t, crypto, "s3cr3t")

	knownHost := &meshapi.KnownHostDTO{Host: "example.com", KeyType: "ssh-rsa", KeyValue: "AAAA"}
	dto := runDetailsDTO(
		withBehavior(DETECT.str()),
		withRepo("/tmp/some-fixture-repo", "modules/x"),
		withRefName("v1.0.0"),
		withSshAuth("fixture-private-key-pem", knownHost),
		withRunToken("custom-run-token"),
		withAsync(),
		withMeshHttpBackendFallback(),
		withPreRunScript("#!/bin/sh\necho hi\n"),
		withPlanArtifact("http://localhost/plan-artifact"),
		withInputs(
			buildingBlockInput("secret", ciphertext, DATA_TYPE_STRING, sensitiveInput()),
			buildingBlockInput("EXPORTED", "value", DATA_TYPE_STRING, envInput()),
		),
	)

	if dto.Spec.Behavior != DETECT.str() {
		t.Errorf("Behavior = %q, want DETECT", dto.Spec.Behavior)
	}
	if dto.Spec.RunToken != "custom-run-token" {
		t.Errorf("RunToken = %q, want custom-run-token", dto.Spec.RunToken)
	}
	if dto.Links.PlanArtifact.Href != "http://localhost/plan-artifact" {
		t.Errorf("PlanArtifact.Href = %q", dto.Links.PlanArtifact.Href)
	}

	var impl meshapi.TerraformImplementation
	if err := json.Unmarshal(dto.Spec.Definition.Spec.Implementation, &impl); err != nil {
		t.Fatalf("unmarshal implementation: %v", err)
	}
	if impl.RepositoryUrl != "/tmp/some-fixture-repo" || impl.RepositoryPath == nil || *impl.RepositoryPath != "modules/x" {
		t.Errorf("repo url/path = %q/%v, want /tmp/some-fixture-repo/modules/x", impl.RepositoryUrl, impl.RepositoryPath)
	}
	if impl.RefName == nil || *impl.RefName != "v1.0.0" {
		t.Errorf("RefName = %v, want v1.0.0", impl.RefName)
	}
	if impl.SshPrivateKey == nil || *impl.SshPrivateKey != "fixture-private-key-pem" {
		t.Errorf("SshPrivateKey = %v", impl.SshPrivateKey)
	}
	if impl.KnownHost == nil || *impl.KnownHost != *knownHost {
		t.Errorf("KnownHost = %v, want %v", impl.KnownHost, knownHost)
	}
	if !impl.Async {
		t.Error("Async = false, want true")
	}
	if !impl.UseMeshHttpBackendFallback {
		t.Error("UseMeshHttpBackendFallback = false, want true")
	}
	if impl.PreRunScript == nil || *impl.PreRunScript != "#!/bin/sh\necho hi\n" {
		t.Errorf("PreRunScript = %v", impl.PreRunScript)
	}

	inputs := dto.Spec.BuildingBlock.Spec.Inputs
	if len(inputs) != 2 {
		t.Fatalf("len(Inputs) = %d, want 2", len(inputs))
	}
	if !inputs[0].IsSensitive || inputs[0].Value != ciphertext {
		t.Errorf("inputs[0] = %+v, want sensitive ciphertext %q", inputs[0], ciphertext)
	}
	if !inputs[1].Env || inputs[1].IsSensitive {
		t.Errorf("inputs[1] = %+v, want env, non-sensitive", inputs[1])
	}

	decrypted, err := crypto.DecryptMeshCertBased(ciphertext)
	if err != nil {
		t.Fatalf("DecryptMeshCertBased: %v", err)
	}
	if decrypted != "s3cr3t" {
		t.Errorf("decrypted = %q, want s3cr3t", decrypted)
	}
}
