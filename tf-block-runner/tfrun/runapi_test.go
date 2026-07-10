package tfrun

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/stretchr/testify/suite"
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

	// setup statically referenced app config
	AppConfig = TfRunnerConfig{
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
			rw.Write(body)
			return

		// this is the updateState call
		case req.Method == http.MethodPatch:
			rw.WriteHeader(200)
			body = GetStatusUpdateResponseForTest()
			rw.Write(body)
			return

		// in all other cases just answer with 200
		default:
			rw.WriteHeader(200)
			rw.Write(make([]byte, 0))
			return
		}
	}))

	// create runApi with basic auth for testing
	sAuth := &runApiAuth{
		baseAuth: meshapi.BasicAuth{Username: "test-username", Password: "test-password"},
	}
	hc := suite.meshfed.Client()
	suite.api = &RunApiClient{
		rid:        AppConfig.RunnerUuid,
		baseURL:    suite.meshfed.URL,
		auth:       sAuth,
		client:     meshapi.NewClientWithHTTP(suite.meshfed.URL, AppConfig.RunnerUuid, sAuth, hc),
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

// setupRequestCapture replaces the test server handler to capture URL and query parameters
// Returns a cleanup function and pointers to the captured values.
func (suite *ApiTestSuite) setupRequestCapture() (cleanup func(), capturedURL *string, capturedQuery *string) {
	originalHandler := suite.meshfed.Config.Handler
	var url, query string

	suite.meshfed.Config.Handler = http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		url = req.URL.Path
		query = req.URL.RawQuery

		// Also capture the request in the caughtRequests slice like the original handler
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

		rw.WriteHeader(200)
		responseBody := GetRunResponseForTest()
		rw.Write(responseBody)
	})

	cleanup = func() { suite.meshfed.Config.Handler = originalHandler }
	return cleanup, &url, &query
}

func (suite *ApiTestSuite) Test_FetchRun() {
	run, err := suite.api.FetchRunDetails("test")

	// make sure a request was made
	suite.Len(suite.caughtRequests, 1)

	// now assert response is parsed correctly:

	// assert basic run fields
	suite.Require().NoError(err)
	suite.Equal("run-uuid", run.Id)
	suite.Equal(APPLY, run.Behavior)
	suite.False(run.IsAsync)
	suite.Equal("buildingBlock-uuid", run.BuildingBlockId)
	suite.Equal("Test BuildingBlock", run.BuildingBlockName)
	suite.Equal("workspace-test", *run.WorkspaceIdentifier)
	suite.Equal("project-test", *run.ProjectIdentifier)
	suite.Equal("platform-test", *run.FullPlatformIdentifier)
	suite.Equal("1.5", run.TerraformVersion)
	suite.NotEmpty(run.RunJsonBase64)

	// assert source
	suite.NotNil(run.Source)
	suite.Equal("https://github.com/meshcloud/forTest.git", run.Source.url)
	suite.Equal("testDirectory", *run.Source.path)
	suite.Equal("testRef", *run.Source.refName)

	// assert auth
	suite.NotNil(run.Source.auth)
	suite.IsType(&SshAuth{}, run.Source.auth)
	auth := run.Source.auth.(*SshAuth)
	suite.Equal("notARealKey", auth.certStr)
	suite.Equal("knownHost", auth.knownHostEntry.host)
	suite.Equal("knownHostValue", auth.knownHostEntry.value)
	suite.Equal("ssh-rsa", auth.knownHostEntry.key)

	// assert vars
	suite.NotEmpty(run.Vars)
	suite.NotNil(run.Vars["input1"])
	suite.Equal("STRING", string(run.Vars["input1"].Type))
	suite.False(run.Vars["input1"].env)
	suite.True(run.Vars["input1"].isSensitive)
	suite.Equal("test1", run.Vars["input1"].value)

	suite.NotNil(run.Vars["input2"])
	suite.Equal("INTEGER", string(run.Vars["input2"].Type))
	suite.True(run.Vars["input2"].env)
	suite.False(run.Vars["input2"].isSensitive)
	suite.InDelta(float64(42), run.Vars["input2"].value, 0)

	suite.NotNil(run.Vars["input3"])
	suite.Equal("BOOLEAN", string(run.Vars["input3"].Type))
	suite.True(run.Vars["input3"].env)
	suite.False(run.Vars["input3"].isSensitive)
	suite.Equal(true, run.Vars["input3"].value)

	suite.NotNil(run.Vars["input4"])
	suite.Equal("LIST", string(run.Vars["input4"].Type))
	suite.False(run.Vars["input4"].env)
	suite.False(run.Vars["input4"].isSensitive)
	suite.IsType(make([]any, 0), run.Vars["input4"].value)

	suite.NotNil(run.Vars["input5"])
	suite.Equal("FILE", string(run.Vars["input5"].Type))
	suite.False(run.Vars["input5"].env)
	suite.False(run.Vars["input5"].isSensitive)
	suite.Equal("fileContent", run.Vars["input5"].value)
}

func (suite *ApiTestSuite) Test_RegisterSource() {
	err := suite.api.Register(
		&RunStatus{
			RunId:            "run-uuid",
			Status:           IN_PROGRESS,
			Summary:          nil,
			CurrentStepIndex: 0,
			Steps: []*StepStatus{
				{
					Name:        "step1",
					DisplayName: "display1",
					Status:      IN_PROGRESS,
				},
				{
					Name:        "step2",
					DisplayName: "display2",
					Status:      PENDING,
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
	json.Unmarshal(req.body, &root)
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
	_, err := suite.api.UpdateState(
		&RunStatus{
			RunId:            "run-uuid",
			Status:           IN_PROGRESS,
			Summary:          message("run summary"),
			CurrentStepIndex: 1,
			Steps: []*StepStatus{
				{
					Name:          "step1",
					DisplayName:   "display1",
					Status:        SUCCEEDED,
					UserMessage:   message("user message for step 1"),
					SystemMessage: message("logs for step 1"),
				},
				{
					Name:          "step2",
					DisplayName:   "display2",
					Status:        IN_PROGRESS,
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
	json.Unmarshal(req.body, &root)
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
	_, err := suite.api.UpdateState(
		&RunStatus{
			RunId:            "run-uuid",
			Status:           IN_PROGRESS,
			Summary:          message("run summary"),
			CurrentStepIndex: 1,
			Steps: []*StepStatus{
				{
					Name:          "step1",
					DisplayName:   "display1",
					Status:        SUCCEEDED,
					UserMessage:   message("user message for step 1"),
					SystemMessage: message("logs for step 1"),
					Outputs: map[string]*TfOutput{
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
	json.Unmarshal(req.body, &root)

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

func (suite *ApiTestSuite) Test_UseCustomPredicate_V2MediaType() {
	// Setup: Enable the useCustomPredicate flag
	originalConfig := AppConfig
	defer func() { AppConfig = originalConfig }()

	useCustomPredicate := "CUSTOM_PREDICATE"
	AppConfig = TfRunnerConfig{
		RunnerUuid: useCustomPredicate,
	}

	// Execute: Make a register call
	err := suite.api.Register(
		&RunStatus{
			RunId:            "run-uuid",
			Status:           IN_PROGRESS,
			Summary:          nil,
			CurrentStepIndex: 0,
			Steps: []*StepStatus{
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

	// Assert: Request uses V1 media type (registration always uses V1 regardless of custom predicate)
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

func (suite *ApiTestSuite) Test_UseCustomPredicate_Null_UsesV1MediaType() {
	// Setup: Test that V1 media type is used
	originalConfig := AppConfig
	defer func() { AppConfig = originalConfig }()

	AppConfig = TfRunnerConfig{
		RunnerUuid: "f1a2b3c4-d5e6-47a8-9b0c-1d2e3f4a5b6c",
	}

	// Execute: Make a register call
	err := suite.api.Register(
		&RunStatus{
			RunId:            "run-uuid",
			Status:           IN_PROGRESS,
			Summary:          nil,
			CurrentStepIndex: 0,
			Steps: []*StepStatus{
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

func (suite *ApiTestSuite) Test_UseCustomPredicate_Empty_UsesV1MediaType() {
	// Setup: useCustomPredicate is explicitly empty string
	originalConfig := AppConfig
	defer func() { AppConfig = originalConfig }()

	useCustomPredicate := ""
	AppConfig = TfRunnerConfig{
		RunnerUuid: useCustomPredicate,
	}

	// Execute: Make a register call
	err := suite.api.Register(
		&RunStatus{
			RunId:            "run-uuid",
			Status:           IN_PROGRESS,
			Summary:          nil,
			CurrentStepIndex: 0,
			Steps: []*StepStatus{
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

func (suite *ApiTestSuite) Test_FetchRunDetails_UseCustomPredicate_UsesCreateEndpoint() {
	// Setup: Enable the useCustomPredicate flag
	originalConfig := AppConfig
	defer func() { AppConfig = originalConfig }()

	useCustomPredicate := "CUSTOM_PREDICATE"
	AppConfig = TfRunnerConfig{
		RunnerUuid: useCustomPredicate,
		RunApiBackend: RunApiConfig{
			User:     "test-user",
			Password: "test-pass",
			Url:      suite.meshfed.URL,
		},
	}

	// Create new API client with updated config
	api := NewRunApi()

	// Temporarily replace the server handler to capture URL details
	cleanup, capturedURL, capturedQuery := suite.setupRequestCapture()
	defer cleanup()

	// Execute: Fetch run details
	_, err := api.FetchRunDetails("test")

	// Assert: No error occurs
	suite.Require().NoError(err)

	// Verify that the URL contains /create endpoint
	suite.Contains(*capturedURL, "/create")
	suite.Contains(*capturedQuery, "forRunnerUuid=CUSTOM_PREDICATE")

	// Assert: Request uses V1 media type when custom predicate is configured
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

func (suite *ApiTestSuite) Test_FetchRunDetails_NoCustomPredicate_UsesDefaultEndpoint() {
	// Setup: Test with valid RunnerUuid (uses new endpoint)
	originalConfig := AppConfig
	defer func() { AppConfig = originalConfig }()

	AppConfig = TfRunnerConfig{
		RunnerUuid: "b2c3d4e5-f6a7-48b9-9c0d-1e2f3a4b5c6d",
		RunApiBackend: RunApiConfig{
			User:     "test-user",
			Password: "test-pass",
			Url:      suite.meshfed.URL,
		},
	}

	// Create new API client with updated config
	api := NewRunApi()

	// Temporarily replace the server handler to capture URL details
	cleanup, capturedURL, capturedQuery := suite.setupRequestCapture()
	defer cleanup()

	// Execute: Fetch run details
	_, err := api.FetchRunDetails("test")

	// Assert: No error occurs
	suite.Require().NoError(err)

	// Verify that the URL contains /create endpoint with forRunnerUuid parameter
	suite.Contains(*capturedURL, "/create")
	suite.Contains(*capturedQuery, "forRunnerUuid=b2c3d4e5-f6a7-48b9-9c0d-1e2f3a4b5c6d")
}

func (suite *ApiTestSuite) Test_ClearRunToken_ResetsToBasicAuth() {
	// Setup: Create API client with basic auth
	basicAuth := base64.StdEncoding.EncodeToString([]byte("test-user:test-pass"))
	tAuth := &runApiAuth{baseAuth: meshapi.BasicAuth{Username: "test-user", Password: "test-pass"}}
	hc := suite.meshfed.Client()
	api := &RunApiClient{
		rid:        "test-runner",
		baseURL:    suite.meshfed.URL,
		auth:       tAuth,
		client:     meshapi.NewClientWithHTTP(suite.meshfed.URL, "test-runner", tAuth, hc),
		httpClient: hc,
	}

	// Reset caught requests
	suite.caughtRequests = make([]*CaughtRequest, 0)

	// Execute: Fetch run details (should use basic auth initially)
	run, err := api.FetchRunDetails("test-node")
	suite.Require().NoError(err)
	suite.NotNil(run)

	// Verify: First request uses Basic auth
	suite.Len(suite.caughtRequests, 1)
	authHeader1 := suite.caughtRequests[0].header["Authorization"]
	suite.Len(authHeader1, 1)
	suite.Equal("Basic "+basicAuth, authHeader1[0])

	// Now the runToken should be set from the fetched run
	suite.NotNil(api.auth.runToken)
	suite.NotEmpty(*api.auth.runToken)

	// Create a status update (should use Bearer token)
	status := &RunStatus{
		RunId:  run.Id,
		Status: IN_PROGRESS,
		Steps:  nil,
	}

	// Reset caught requests for next call
	suite.caughtRequests = make([]*CaughtRequest, 0)

	// Execute: Update state (should use Bearer token)
	_, err = api.UpdateState(status)
	suite.Require().NoError(err)

	// Verify: Second request uses Bearer token
	suite.Len(suite.caughtRequests, 1)
	authHeader2 := suite.caughtRequests[0].header["Authorization"]
	suite.Len(authHeader2, 1)
	suite.Contains(authHeader2[0], "Bearer ")
	suite.NotEqual("Basic "+basicAuth, authHeader2[0])

	// Execute: Clear the run token
	api.ClearRunToken()

	// Verify: runToken is now nil
	suite.Nil(api.auth.runToken)

	// Reset caught requests for next call
	suite.caughtRequests = make([]*CaughtRequest, 0)

	// Execute: Fetch next run (should use basic auth again)
	run2, err := api.FetchRunDetails("test-node-2")
	suite.Require().NoError(err)
	suite.NotNil(run2)

	// Verify: Third request uses Basic auth again
	suite.Len(suite.caughtRequests, 1)
	authHeader3 := suite.caughtRequests[0].header["Authorization"]
	suite.Len(authHeader3, 1)
	suite.Equal("Basic "+basicAuth, authHeader3[0])

	// Verify: runToken is set again from the new run
	suite.NotNil(api.auth.runToken)
	suite.NotEmpty(*api.auth.runToken)
}

func (suite *ApiTestSuite) Test_ClearRunToken_MultipleRunCycle() {
	// This test verifies the complete cycle: fetch -> use token -> clear -> fetch again
	// Simulating multiple runs being processed by a worker

	basicAuth := base64.StdEncoding.EncodeToString([]byte("worker-user:worker-pass"))
	wAuth := &runApiAuth{baseAuth: meshapi.BasicAuth{Username: "worker-user", Password: "worker-pass"}}
	_ = basicAuth // kept for assertion comparisons below
	hc := suite.meshfed.Client()
	api := &RunApiClient{
		rid:        "worker-001",
		baseURL:    suite.meshfed.URL,
		auth:       wAuth,
		client:     meshapi.NewClientWithHTTP(suite.meshfed.URL, "worker-001", wAuth, hc),
		httpClient: hc,
	}

	for i := 1; i <= 3; i++ {
		// Reset caught requests for each iteration
		suite.caughtRequests = make([]*CaughtRequest, 0)

		// Fetch run (should always use basic auth at start of each cycle)
		run, err := api.FetchRunDetails("worker-node")
		suite.Require().NoError(err, "Iteration %d: FetchRunDetails should not error", i)
		suite.NotNil(run, "Iteration %d: Run should not be nil", i)

		// Verify fetch used Basic auth
		suite.Len(suite.caughtRequests, 1, "Iteration %d: Should have one request", i)
		fetchAuthHeader := suite.caughtRequests[0].header["Authorization"]
		suite.Equal("Basic "+basicAuth, fetchAuthHeader[0],
			"Iteration %d: Fetch should use Basic auth", i)

		// Verify runToken is set
		suite.NotNil(api.auth.runToken, "Iteration %d: runToken should be set after fetch", i)

		// Simulate run operations (Register, UpdateState) - should use Bearer token
		suite.caughtRequests = make([]*CaughtRequest, 0)

		status := &RunStatus{
			RunId:  run.Id,
			Status: IN_PROGRESS,
			Steps:  nil,
		}
		_, err = api.UpdateState(status)
		suite.Require().NoError(err, "Iteration %d: UpdateState should not error", i)

		// Verify update used Bearer token
		updateAuthHeader := suite.caughtRequests[0].header["Authorization"]
		suite.Contains(updateAuthHeader[0], "Bearer ",
			"Iteration %d: Update should use Bearer token", i)

		// Clear token after run completes (simulating worker behavior)
		api.ClearRunToken()
		suite.Nil(api.auth.runToken,
			"Iteration %d: runToken should be nil after clearing", i)
	}
}
