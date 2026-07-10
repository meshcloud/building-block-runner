package tf

// CP2 (PLAN_DETAIL_01_tf_characterization_tests.md §9): header pins + the UpdateState malformed-
// response branch. Kept in its own file (disjoint from runapi_test.go) so this checkpoint lands
// independently; it reuses ApiTestSuite's shared meshfed test server (SetupSuite in runapi_test.go)
// rather than duplicating suite setup.

import (
	"net/http"
)

// Test_FetchRunDetails_SetsRunnerHeadersAndNodeId pins D9 pin 7 (media types + runner headers,
// meshapi/client.go:235-243) for the parts runapi_test.go's Test_FetchRun does not assert:
// User-Agent, the X-Meshcloud-Runner-* pair, and the "<rid>-<postfix>" node-id suffix
// (runapi.go:87) that FetchRunDetails builds before delegating to the shared meshapi.Client.
// No test in this package calls meshapi.SetClientMetadata, so the package-level defaults
// ("unknown-runner"/"dev", client.go:27-28) hold for the whole suite run.
func (suite *ApiTestSuite) Test_FetchRunDetails_SetsRunnerHeadersAndNodeId() {
	_, err := suite.api.FetchRunDetails("cp2-node")
	suite.Require().NoError(err)
	suite.Require().Len(suite.caughtRequests, 1)

	req := suite.caughtRequests[0]

	suite.Equal([]string{"meshcloud-unknown-runner/dev"}, req.header["User-Agent"])
	suite.Equal([]string{"unknown-runner"}, req.header["X-Meshcloud-Runner-Name"])
	suite.Equal([]string{"dev"}, req.header["X-Meshcloud-Runner-Version"])

	// requester = "<rid>-<nodePostfix>" (runapi.go:87); rid is AppConfig.RunnerUuid ("runApi_test",
	// set by ApiTestSuite.SetupSuite).
	suite.Equal([]string{"runApi_test-cp2-node"}, req.header["X-Block-Runner-Node-Id"])
}

// Test_UpdateState_MalformedResponseBodyReturnsError pins the unmarshal-failure branch of
// UpdateState (runapi.go:140-143): a 2xx PatchStatus response whose body isn't the expected
// RunUpdateResponseDTO JSON must surface as an error, not a silently-false abort flag.
func (suite *ApiTestSuite) Test_UpdateState_MalformedResponseBodyReturnsError() {
	original := suite.meshfed.Config.Handler
	suite.meshfed.Config.Handler = http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("not-json"))
	})
	suite.T().Cleanup(func() { suite.meshfed.Config.Handler = original })

	abort, err := suite.api.UpdateState(&RunStatus{RunId: "run-uuid", Status: IN_PROGRESS})

	suite.Require().Error(err)
	suite.False(abort)
}
