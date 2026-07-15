package tf

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	meshapi "github.com/meshcloud/building-block-runner/internal/meshapi"
	"github.com/meshcloud/building-block-runner/internal/report"
)

type ApiTestSuite struct {
	suite.Suite
	meshfed        *httptest.Server
	api            RunApi
	caughtRequests []*CaughtRequest
}

type CaughtRequest struct {
	header map[string][]string
	body   []byte
}

func Test_ApiSuite(t *testing.T) {
	suite.Run(t, new(ApiTestSuite))
}

// setup test server and a real RunApi client.
func (suite *ApiTestSuite) SetupSuite() {

	// runner config threaded into the RunApi client under test
	cfg := TfRunnerConfig{
		RunnerUuid: "runApi_test",
		RunApiBackend: RunApiConfig{
			Url:      "http://test-url",
			User:     "test-user",
			Password: "test-password",
		},
	}

	// setup suite

	suite.caughtRequests = make([]*CaughtRequest, 0)
	suite.meshfed = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {

		// all requests against the test server will be caught for later inspection
		caughtRequest := CaughtRequest{
			header: make(map[string][]string),
		}
		for name, values := range req.Header {
			vals := make([]string, 0)
			vals = append(vals, values...)
			caughtRequest.header[name] = vals
		}

		body, err := io.ReadAll(req.Body)
		if err == nil {
			caughtRequest.body = body
		}

		suite.caughtRequests = append(suite.caughtRequests, &caughtRequest)

		switch {
		// this is the "get run" which rather creates a run to work with
		// we mock a server response to be able to inspect the response handling on tfRunner side
		case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "meshbuildingblockruns"):
			rw.WriteHeader(200)
			body := GetRunResponseForTest()
			_, _ = rw.Write(body)
			return

		// this is the updateState call
		case req.Method == http.MethodPatch:
			rw.WriteHeader(200)
			body = GetStatusUpdateResponseForTest()
			_, _ = rw.Write(body)
			return

		// in all other cases just answer with 200
		default:
			rw.WriteHeader(200)
			_, _ = rw.Write(make([]byte, 0))
			return
		}
	}))

	// create runApi with basic auth for testing
	sAuth := &runApiAuth{
		baseAuth: meshapi.BasicAuth{Username: "test-username", Password: "test-password"},
	}
	hc := suite.meshfed.Client()
	suite.api = &RunApiClient{
		rid:        cfg.RunnerUuid,
		baseURL:    suite.meshfed.URL,
		auth:       sAuth,
		client:     meshapi.NewClientWithHTTP(suite.meshfed.URL, cfg.RunnerUuid, sAuth, hc),
		httpClient: hc,
	}
}

// reset caught requests after each test.
func (suite *ApiTestSuite) TearDownTest() {
	suite.caughtRequests = make([]*CaughtRequest, 0)
}

func (suite *ApiTestSuite) TearDownSuite() {
	suite.meshfed.Close()
}

func (suite *ApiTestSuite) Test_RegisterSource() {
	err := suite.api.Register(report.RunStatus{
		RunId:            "run-uuid",
		Status:           report.IN_PROGRESS,
		Summary:          nil,
		CurrentStepIndex: 0,
		Steps: []report.StepStatus{
			{
				Name:        "step1",
				DisplayName: "display1",
				Status:      report.IN_PROGRESS,
			},
			{
				Name:        "step2",
				DisplayName: "display2",
				Status:      report.PENDING,
			},
			{
				Name:        "step3",
				DisplayName: "display3",
			},
		},
	},
	)
	// assert that no error occurs
	suite.Require().NoError(err)

	// assert that one request was done
	suite.Len(suite.caughtRequests, 1)

	// now assert that request looks as expected
	req := suite.caughtRequests[0]

	// assert headers
	acceptHeader, exists := req.header["Accept"]
	suite.True(exists)
	suite.Len(acceptHeader, 1)
	suite.Equal(meshapi.BlockRunMediaTypeV1, acceptHeader[0])

	contentTypeHeader, exists := req.header["Content-Type"]
	suite.True(exists)
	suite.Len(contentTypeHeader, 1)
	suite.Equal(meshapi.BlockRunMediaTypeV1, contentTypeHeader[0])

	runnerIdHeader, exists := req.header["X-Block-Runner-Node-Id"]
	suite.True(exists)
	suite.Len(runnerIdHeader, 1)
	suite.Equal("runApi_test", runnerIdHeader[0])

	// assert body
	root := any(nil)
	suite.Require().NoError(json.Unmarshal(req.body, &root))
	assertJsonExists(suite.T(), root, "$.source")
	assertJsonEqual(suite.T(), root, "$.source.id", "runApi_test")
	assertJsonNotExists(suite.T(), root, "$.source.externalId")
	assertJsonNotExists(suite.T(), root, "$.source.externalUrl")

	assertJsonExists(suite.T(), root, "$.steps")
	assertJsonLen(suite.T(), root, "$.steps[*]", 3)

	assertJsonEqual(suite.T(), root, "$.steps[0].id", "step1")
	assertJsonEqual(suite.T(), root, "$.steps[0].displayName", "display1")
	assertJsonNotExists(suite.T(), root, "$.steps[0].status")

	assertJsonEqual(suite.T(), root, "$.steps[1].id", "step2")
	assertJsonEqual(suite.T(), root, "$.steps[1].displayName", "display2")
	assertJsonNotExists(suite.T(), root, "$.steps[1].status")

	assertJsonEqual(suite.T(), root, "$.steps[2].id", "step3")
	assertJsonEqual(suite.T(), root, "$.steps[2].displayName", "display3")
	assertJsonNotExists(suite.T(), root, "$.steps[2].status")
}

func (suite *ApiTestSuite) Test_UpdateState() {
	_, err := suite.api.Report(report.RunStatus{
		RunId:            "run-uuid",
		Status:           report.IN_PROGRESS,
		Summary:          message("run summary"),
		CurrentStepIndex: 1,
		Steps: []report.StepStatus{
			{
				Name:          "step1",
				DisplayName:   "display1",
				Status:        report.SUCCEEDED,
				UserMessage:   message("user message for step 1"),
				SystemMessage: message("logs for step 1"),
			},
			{
				Name:          "step2",
				DisplayName:   "display2",
				Status:        report.IN_PROGRESS,
				UserMessage:   nil,
				SystemMessage: message("logs for step 2"),
			},
			{
				Name:        "step3",
				DisplayName: "display3",
			},
		},
	},
	)
	// assert that no error occurs
	suite.Require().NoError(err)

	// assert that one request was done
	suite.Len(suite.caughtRequests, 1)

	// now assert that request looks as expected
	req := suite.caughtRequests[0]

	// assert headers
	acceptHeader, exists := req.header["Accept"]
	suite.True(exists)
	suite.Len(acceptHeader, 1)
	suite.Equal(meshapi.BlockRunMediaTypeV1, acceptHeader[0])

	contentTypeHeader, exists := req.header["Content-Type"]
	suite.True(exists)
	suite.Len(contentTypeHeader, 1)
	suite.Equal(meshapi.BlockRunMediaTypeV1, contentTypeHeader[0])

	runnerIdHeader, exists := req.header["X-Block-Runner-Node-Id"]
	suite.True(exists)
	suite.Len(runnerIdHeader, 1)
	suite.Equal("runApi_test", runnerIdHeader[0])

	// assert body
	root := any(nil)
	suite.Require().NoError(json.Unmarshal(req.body, &root))
	assertJsonEqual(suite.T(), root, "$.blockRunId", "run-uuid")
	assertJsonEqual(suite.T(), root, "$.source", "runApi_test")
	assertJsonEqual(suite.T(), root, "$.status", "IN_PROGRESS")
	assertJsonEqual(suite.T(), root, "$.summary", "run summary")
	assertJsonEqual(suite.T(), root, "$.type", string(meshapi.RunTypeTerraform))

	assertJsonExists(suite.T(), root, "$.steps")
	assertJsonLen(suite.T(), root, "$.steps[*]", 3)

	assertJsonEqual(suite.T(), root, "$.steps[0].id", "step1")
	assertJsonEqual(suite.T(), root, "$.steps[0].displayName", "display1")
	assertJsonEqual(suite.T(), root, "$.steps[0].status", "SUCCEEDED")
	assertJsonEqual(suite.T(), root, "$.steps[0].userMessage", "user message for step 1")
	assertJsonEqual(suite.T(), root, "$.steps[0].systemMessage", "logs for step 1")

	assertJsonEqual(suite.T(), root, "$.steps[1].id", "step2")
	assertJsonEqual(suite.T(), root, "$.steps[1].displayName", "display2")
	assertJsonEqual(suite.T(), root, "$.steps[1].status", "IN_PROGRESS")
	assertJsonNotExists(suite.T(), root, "$.steps[1].userMessage")
	assertJsonEqual(suite.T(), root, "$.steps[1].systemMessage", "logs for step 2")

	assertJsonEqual(suite.T(), root, "$.steps[2].id", "step3")
	assertJsonEqual(suite.T(), root, "$.steps[2].displayName", "display3")
	assertJsonEqual(suite.T(), root, "$.steps[2].status", "PENDING")
	assertJsonNotExists(suite.T(), root, "$.steps[2].userMessage")
	assertJsonNotExists(suite.T(), root, "$.steps[2].systemMessage")
}

func (suite *ApiTestSuite) Test_UpdateStateOutputs() {
	_, err := suite.api.Report(report.RunStatus{
		RunId:            "run-uuid",
		Status:           report.IN_PROGRESS,
		Summary:          message("run summary"),
		CurrentStepIndex: 1,
		Steps: []report.StepStatus{
			{
				Name:          "step1",
				DisplayName:   "display1",
				Status:        report.SUCCEEDED,
				UserMessage:   message("user message for step 1"),
				SystemMessage: message("logs for step 1"),
				Outputs: map[string]report.Output{
					"test1": {
						Type:  DATA_TYPE_BOOLEAN,
						Value: true,
					},
					"test2": {
						Type:  DATA_TYPE_CODE,
						Value: `["foo", "bar"]`,
					},
				},
			},
		},
	},
	)
	// assert that no error occurs
	suite.Require().NoError(err)

	// assert that one request was done
	suite.Len(suite.caughtRequests, 1)

	// now assert that request looks as expected
	req := suite.caughtRequests[0]

	// assert body, but only outputs here specifically.
	root := any(nil)
	suite.Require().NoError(json.Unmarshal(req.body, &root))

	assertJsonExists(suite.T(), root, "$.steps")
	assertJsonLen(suite.T(), root, "$.steps[*]", 1)

	assertJsonEqual(suite.T(), root, "$.steps[0].id", "step1")
	assertJsonEqual(suite.T(), root, "$.steps[0].outputs.test1.value", true)
	assertJsonEqual(suite.T(), root, "$.steps[0].outputs.test1.type", "BOOLEAN")
	assertJsonEqual(suite.T(), root, "$.steps[0].outputs.test2.value", `["foo", "bar"]`)
	assertJsonEqual(suite.T(), root, "$.steps[0].outputs.test2.type", "CODE")
}

func GetStatusUpdateResponseForTest() []byte {
	body, _ := json.Marshal(
		&meshapi.RunUpdateResponseDTO{
			Abort: false,
		},
	)
	return body
}

func GetRunResponseForTest() []byte {
	return []byte(`
{
	"apiVersion": "v1",
	"kind": "MeshBuildingBlockRun",
	"metadata": {
    "uuid": "run-uuid"
	},
	"spec": {
	  "runNumber": 1000,
    "behavior": "APPLY",
		"runToken": "test-run-token-12345",
		"buildingBlock": {
		  "uuid": "buildingBlock-uuid",
			"spec": {
			  "displayName": "Test BuildingBlock",
				"workspaceIdentifier": "workspace-test",
				"projectIdentifier": "project-test",
				"fullPlatformIdentifier": "platform-test",
				"inputs" : [
				  {
				    "key": "input1",
						"value": "test1",
						"type": "STRING",
						"isSensitive": true,
						"isEnvironment": false
				  },
				  {
				    "key": "input2",
						"value": 42,
						"type": "INTEGER",
						"isSensitive": false,
						"isEnvironment": true
				  },
					{
				    "key": "input3",
						"value": true,
						"type": "BOOLEAN",
						"isSensitive": false,
						"isEnvironment": true
				  },
					{
				    "key": "input4",
						"value": [],
						"type": "LIST",
						"isSensitive": false,
						"isEnvironment": false
				  },
					{
				    "key": "input5",
						"value": "fileContent",
						"type": "FILE",
						"isSensitive": false,
						"isEnvironment": false
				  }
				]
			}
		},
		"buildingBlockDefinition": {
		  "uuid" : "definition-uuid",
			"spec" : {
			  "version": 1,
				"implementation": {
          "terraformVersion": "1.5",
					"repositoryUrl": "https://github.com/meshcloud/forTest.git",
					"repositoryPath": "testDirectory",
					"refName": "testRef",
					"sshPrivateKey": "notARealKey",
					"knownHost": {
					  "host": "knownHost",
						"keyType": "ssh-rsa",
						"keyValue": "knownHostValue"
					},
					"async": false
				}
			}
		}
	},
	"_links": {
	  "registerSource": {
		  "href": ""
		},
		"updateSource": {
		  "href": ""
		},
		"meshstackBaseUrl": {
		  "href": ""
		}
	}
}
	`)
}

// These were named
// Test_UseCustomPredicate_* for a "custom predicate" feature that exists nowhere in
// production code (grep finds it only in this file, pre-rename). What they actually pin:
// Register always requests the V1 media type, regardless of whether RunnerUuid is a
// real UUID, an arbitrary string, or empty.
func (suite *ApiTestSuite) Test_Register_UsesV1MediaType_ForNonUuidRunnerUuid() {
	// Setup: RunnerUuid is an arbitrary non-UUID string, threaded via the client's rid
	client, ok := suite.api.(*RunApiClient)
	suite.Require().True(ok)
	originalRid := client.rid
	defer func() { client.rid = originalRid }()

	client.rid = "CUSTOM_PREDICATE"

	// Execute: Make a register call
	err := suite.api.Register(report.RunStatus{
		RunId:            "run-uuid",
		Status:           report.IN_PROGRESS,
		Summary:          nil,
		CurrentStepIndex: 0,
		Steps: []report.StepStatus{
			{
				Name:        "step1",
				DisplayName: "display1",
			},
		},
	},
	)

	// Assert: No error occurs
	suite.Require().NoError(err)

	// Assert: One request was made
	suite.Len(suite.caughtRequests, 1)

	// Assert: Request uses V1 media type (registration always uses V1, regardless of RunnerUuid shape)
	req := suite.caughtRequests[0]
	acceptHeader, exists := req.header["Accept"]
	suite.True(exists)
	suite.Len(acceptHeader, 1)
	suite.Equal(meshapi.BlockRunMediaTypeV1, acceptHeader[0])

	contentTypeHeader, exists := req.header["Content-Type"]
	suite.True(exists)
	suite.Len(contentTypeHeader, 1)
	suite.Equal(meshapi.BlockRunMediaTypeV1, contentTypeHeader[0])
}

func (suite *ApiTestSuite) Test_Register_UsesV1MediaType_ForUuidRunnerUuid() {
	// Setup: RunnerUuid is a real UUID, threaded via the client's rid
	client, ok := suite.api.(*RunApiClient)
	suite.Require().True(ok)
	originalRid := client.rid
	defer func() { client.rid = originalRid }()

	client.rid = "f1a2b3c4-d5e6-47a8-9b0c-1d2e3f4a5b6c"

	// Execute: Make a register call
	err := suite.api.Register(report.RunStatus{
		RunId:            "run-uuid",
		Status:           report.IN_PROGRESS,
		Summary:          nil,
		CurrentStepIndex: 0,
		Steps: []report.StepStatus{
			{
				Name:        "step1",
				DisplayName: "display1",
			},
		},
	},
	)

	// Assert: No error occurs
	suite.Require().NoError(err)

	// Assert: One request was made
	suite.Len(suite.caughtRequests, 1)

	// Assert: Request uses V1 media type (default behavior)
	req := suite.caughtRequests[0]
	acceptHeader, exists := req.header["Accept"]
	suite.True(exists)
	suite.Len(acceptHeader, 1)
	suite.Equal(meshapi.BlockRunMediaTypeV1, acceptHeader[0])

	contentTypeHeader, exists := req.header["Content-Type"]
	suite.True(exists)
	suite.Len(contentTypeHeader, 1)
	suite.Equal(meshapi.BlockRunMediaTypeV1, contentTypeHeader[0])
}

func (suite *ApiTestSuite) Test_Register_UsesV1MediaType_ForEmptyRunnerUuid() {
	// Setup: RunnerUuid is explicitly an empty string, threaded via the client's rid
	client, ok := suite.api.(*RunApiClient)
	suite.Require().True(ok)
	originalRid := client.rid
	defer func() { client.rid = originalRid }()

	client.rid = ""

	// Execute: Make a register call
	err := suite.api.Register(report.RunStatus{
		RunId:            "run-uuid",
		Status:           report.IN_PROGRESS,
		Summary:          nil,
		CurrentStepIndex: 0,
		Steps: []report.StepStatus{
			{
				Name:        "step1",
				DisplayName: "display1",
			},
		},
	},
	)

	// Assert: No error occurs
	suite.Require().NoError(err)

	// Assert: One request was made
	suite.Len(suite.caughtRequests, 1)

	// Assert: Request uses V1 media type
	req := suite.caughtRequests[0]
	acceptHeader, exists := req.header["Accept"]
	suite.True(exists)
	suite.Len(acceptHeader, 1)
	suite.Equal(meshapi.BlockRunMediaTypeV1, acceptHeader[0])

	contentTypeHeader, exists := req.header["Content-Type"]
	suite.True(exists)
	suite.Len(contentTypeHeader, 1)
	suite.Equal(meshapi.BlockRunMediaTypeV1, contentTypeHeader[0])
}

// Test_UpdateState_UsesRunTokenBearerAuth pins that a RunApi constructed with a runToken
// authenticates run-scoped calls (UpdateState) as that run via Bearer auth, taking priority over
// the backend's fallback basic auth -- the run-scoped, construction-time token model that
// replaced the deleted mutable SetRunToken/ClearRunToken slot.
func (suite *ApiTestSuite) Test_UpdateState_UsesRunTokenBearerAuth() {
	api := NewRunApi(
		RunApiConfig{
			Url:      suite.meshfed.URL,
			User:     "test-user",
			Password: "test-pass",
		},
		"test-runner",
		"per-run-token-abc",
	)

	suite.caughtRequests = make([]*CaughtRequest, 0)

	_, err := api.Report(report.RunStatus{RunId: "run-uuid", Status: report.IN_PROGRESS})
	suite.Require().NoError(err)

	suite.Require().Len(suite.caughtRequests, 1)
	authHeader := suite.caughtRequests[0].header["Authorization"]
	suite.Require().Len(authHeader, 1)
	suite.Equal("Bearer per-run-token-abc", authHeader[0])
}

// Test_UpdateState_EmptyRunToken_FallsBackToBasicAuth pins the "" runToken waiver: a RunApi
// built with no runToken (single-run mode's no-op auth) falls back to the backend's configured
// basic auth rather than sending an empty Bearer token.
func (suite *ApiTestSuite) Test_UpdateState_EmptyRunToken_FallsBackToBasicAuth() {
	basicAuth := base64.StdEncoding.EncodeToString([]byte("test-user:test-pass"))
	api := NewRunApi(
		RunApiConfig{
			Url:      suite.meshfed.URL,
			User:     "test-user",
			Password: "test-pass",
		},
		"test-runner",
		"",
	)

	suite.caughtRequests = make([]*CaughtRequest, 0)

	_, err := api.Report(report.RunStatus{RunId: "run-uuid", Status: report.IN_PROGRESS})
	suite.Require().NoError(err)

	suite.Require().Len(suite.caughtRequests, 1)
	authHeader := suite.caughtRequests[0].header["Authorization"]
	suite.Require().Len(authHeader, 1)
	suite.Equal("Basic "+basicAuth, authHeader[0])
}
