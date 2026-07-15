package tf

// Header pins + the Report malformed-
// response branch. Kept in its own file (disjoint from runapi_test.go) so it lands
// independently; it reuses ApiTestSuite's shared meshfed test server (SetupSuite in runapi_test.go)
// rather than duplicating suite setup.

import (
	"net/http"

	"github.com/meshcloud/building-block-runner/internal/report"
)

// Test_Report_MalformedResponseBodyReturnsError pins the unmarshal-failure branch of
// RunApiClient.Report: a 2xx PatchStatus response whose body isn't the expected
// RunUpdateResponseDTO JSON must surface as an error, not a silently-false abort flag.
func (suite *ApiTestSuite) Test_Report_MalformedResponseBodyReturnsError() {
	original := suite.meshfed.Config.Handler
	suite.meshfed.Config.Handler = http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("not-json"))
	})
	suite.T().Cleanup(func() { suite.meshfed.Config.Handler = original })

	abort, err := suite.api.Report(report.RunStatus{RunId: "run-uuid", Status: report.IN_PROGRESS})

	suite.Require().Error(err)
	suite.False(abort)
}
