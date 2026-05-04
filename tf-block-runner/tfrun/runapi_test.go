package tfrun

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
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

// setup test server and a real RunApi client
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
		basic: meshapi.BasicAuth{Username: "test-username", Password: "test-password"}.AuthHeader(),
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

// reset caught requests after each test
func (suite *ApiTestSuite) TearDownTest() {
	suite.caughtRequests = make([]*CaughtRequest, 0)
}

func (suite *ApiTestSuite) TearDownSuite() {
	suite.meshfed.Close()
}

// setupRequestCapture replaces the test server handler to capture URL and query parameters
// Returns a cleanup function and pointers to the captured values
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
	assert.Len(suite.T(), suite.caughtRequests, 1)

	// now assert response is parsed correctly:

	// assert basic run fields
	assert.Nil(suite.T(), err)
	assert.Equal(suite.T(), "run-uuid", run.Id)
	assert.Equal(suite.T(), APPLY, run.Behavior)
	assert.Equal(suite.T(), false, run.IsAsync)
	assert.Equal(suite.T(), "buildingBlock-uuid", run.BuildingBlockId)
	assert.Equal(suite.T(), "Test BuildingBlock", run.BuildingBlockName)
	assert.Equal(suite.T(), "workspace-test", *run.WorkspaceIdentifier)
	assert.Equal(suite.T(), "project-test", *run.ProjectIdentifier)
	assert.Equal(suite.T(), "platform-test", *run.FullPlatformIdentifier)
	assert.Equal(suite.T(), "1.5", run.TerraformVersion)
	assert.NotEmpty(suite.T(), run.RunJsonBase64)

	// assert source
	assert.NotNil(suite.T(), run.Source)
	assert.Equal(suite.T(), "https://github.com/meshcloud/forTest.git", run.Source.url)
	assert.Equal(suite.T(), "testDirectory", *run.Source.path)
	assert.Equal(suite.T(), "testRef", *run.Source.refName)

	// assert auth
	assert.NotNil(suite.T(), run.Source.auth)
	assert.IsType(suite.T(), &SshAuth{}, run.Source.auth)
	auth := run.Source.auth.(*SshAuth)
	assert.Equal(suite.T(), "notARealKey", auth.certStr)
	assert.Equal(suite.T(), "knownHost", auth.knownHostEntry.host)
	assert.Equal(suite.T(), "knownHostValue", auth.knownHostEntry.value)
	assert.Equal(suite.T(), "ssh-rsa", auth.knownHostEntry.key)

	// assert vars
	assert.NotEmpty(suite.T(), run.Vars)
	assert.NotNil(suite.T(), run.Vars["input1"])
	assert.Equal(suite.T(), "STRING", string(run.Vars["input1"].Type))
	assert.Equal(suite.T(), false, run.Vars["input1"].env)
	assert.Equal(suite.T(), true, run.Vars["input1"].isSensitive)
	assert.Equal(suite.T(), "test1", run.Vars["input1"].value)

	assert.NotNil(suite.T(), run.Vars["input2"])
	assert.Equal(suite.T(), "INTEGER", string(run.Vars["input2"].Type))
	assert.Equal(suite.T(), true, run.Vars["input2"].env)
	assert.Equal(suite.T(), false, run.Vars["input2"].isSensitive)
	assert.Equal(suite.T(), float64(42), run.Vars["input2"].value)

	assert.NotNil(suite.T(), run.Vars["input3"])
	assert.Equal(suite.T(), "BOOLEAN", string(run.Vars["input3"].Type))
	assert.Equal(suite.T(), true, run.Vars["input3"].env)
	assert.Equal(suite.T(), false, run.Vars["input3"].isSensitive)
	assert.Equal(suite.T(), true, run.Vars["input3"].value)

	assert.NotNil(suite.T(), run.Vars["input4"])
	assert.Equal(suite.T(), "LIST", string(run.Vars["input4"].Type))
	assert.Equal(suite.T(), false, run.Vars["input4"].env)
	assert.Equal(suite.T(), false, run.Vars["input4"].isSensitive)
	assert.IsType(suite.T(), make([]any, 0), run.Vars["input4"].value)

	assert.NotNil(suite.T(), run.Vars["input5"])
	assert.Equal(suite.T(), "FILE", string(run.Vars["input5"].Type))
	assert.Equal(suite.T(), false, run.Vars["input5"].env)
	assert.Equal(suite.T(), false, run.Vars["input5"].isSensitive)
	assert.Equal(suite.T(), "fileContent", run.Vars["input5"].value)
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
	assert.Nil(suite.T(), err)

	// assert that one request was done
	assert.Len(suite.T(), suite.caughtRequests, 1)

	// now assert that request looks as expected
	req := suite.caughtRequests[0]

	// assert headers
	acceptHeader, exists := req.header["Accept"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), acceptHeader, 1)
	assert.Equal(suite.T(), meshapi.BlockRunMediaTypeV1, acceptHeader[0])

	contentTypeHeader, exists := req.header["Content-Type"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), contentTypeHeader, 1)
	assert.Equal(suite.T(), meshapi.BlockRunMediaTypeV1, contentTypeHeader[0])

	runnerIdHeader, exists := req.header["X-Block-Runner-Node-Id"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), runnerIdHeader, 1)
	assert.Equal(suite.T(), "runApi_test", runnerIdHeader[0])

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
	assert.Nil(suite.T(), err)

	// assert that one request was done
	assert.Len(suite.T(), suite.caughtRequests, 1)

	// now assert that request looks as expected
	req := suite.caughtRequests[0]

	// assert headers
	acceptHeader, exists := req.header["Accept"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), acceptHeader, 1)
	assert.Equal(suite.T(), meshapi.BlockRunMediaTypeV1, acceptHeader[0])

	contentTypeHeader, exists := req.header["Content-Type"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), contentTypeHeader, 1)
	assert.Equal(suite.T(), meshapi.BlockRunMediaTypeV1, contentTypeHeader[0])

	runnerIdHeader, exists := req.header["X-Block-Runner-Node-Id"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), runnerIdHeader, 1)
	assert.Equal(suite.T(), "runApi_test", runnerIdHeader[0])

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
	assert.Nil(suite.T(), err)

	// assert that one request was done
	assert.Len(suite.T(), suite.caughtRequests, 1)

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
	assert.Nil(suite.T(), err)

	// Assert: One request was made
	assert.Len(suite.T(), suite.caughtRequests, 1)

	// Assert: Request uses V1 media type (registration always uses V1 regardless of custom predicate)
	req := suite.caughtRequests[0]
	acceptHeader, exists := req.header["Accept"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), acceptHeader, 1)
	assert.Equal(suite.T(), meshapi.BlockRunMediaTypeV1, acceptHeader[0])

	contentTypeHeader, exists := req.header["Content-Type"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), contentTypeHeader, 1)
	assert.Equal(suite.T(), meshapi.BlockRunMediaTypeV1, contentTypeHeader[0])
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
	assert.Nil(suite.T(), err)

	// Assert: One request was made
	assert.Len(suite.T(), suite.caughtRequests, 1)

	// Assert: Request uses V1 media type (default behavior)
	req := suite.caughtRequests[0]
	acceptHeader, exists := req.header["Accept"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), acceptHeader, 1)
	assert.Equal(suite.T(), meshapi.BlockRunMediaTypeV1, acceptHeader[0])

	contentTypeHeader, exists := req.header["Content-Type"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), contentTypeHeader, 1)
	assert.Equal(suite.T(), meshapi.BlockRunMediaTypeV1, contentTypeHeader[0])
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
	assert.Nil(suite.T(), err)

	// Assert: One request was made
	assert.Len(suite.T(), suite.caughtRequests, 1)

	// Assert: Request uses V1 media type
	req := suite.caughtRequests[0]
	acceptHeader, exists := req.header["Accept"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), acceptHeader, 1)
	assert.Equal(suite.T(), meshapi.BlockRunMediaTypeV1, acceptHeader[0])

	contentTypeHeader, exists := req.header["Content-Type"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), contentTypeHeader, 1)
	assert.Equal(suite.T(), meshapi.BlockRunMediaTypeV1, contentTypeHeader[0])
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
	assert.Nil(suite.T(), err)

	// Verify that the URL contains /create endpoint
	assert.Contains(suite.T(), *capturedURL, "/create")
	assert.Contains(suite.T(), *capturedQuery, "forRunnerUuid=CUSTOM_PREDICATE")

	// Assert: Request uses V1 media type when custom predicate is configured
	req := suite.caughtRequests[0]
	acceptHeader, exists := req.header["Accept"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), acceptHeader, 1)
	assert.Equal(suite.T(), meshapi.BlockRunMediaTypeV1, acceptHeader[0])

	contentTypeHeader, exists := req.header["Content-Type"]
	assert.True(suite.T(), exists)
	assert.Len(suite.T(), contentTypeHeader, 1)
	assert.Equal(suite.T(), meshapi.BlockRunMediaTypeV1, contentTypeHeader[0])
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
	assert.Nil(suite.T(), err)

	// Verify that the URL contains /create endpoint with forRunnerUuid parameter
	assert.Contains(suite.T(), *capturedURL, "/create")
	assert.Contains(suite.T(), *capturedQuery, "forRunnerUuid=b2c3d4e5-f6a7-48b9-9c0d-1e2f3a4b5c6d")
}

func (suite *ApiTestSuite) Test_ClearRunToken_ResetsToBasicAuth() {
	// Setup: Create API client with basic auth
	basicAuth := base64.StdEncoding.EncodeToString([]byte("test-user:test-pass"))
	tAuth := &runApiAuth{basic: "Basic " + basicAuth}
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
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), run)

	// Verify: First request uses Basic auth
	assert.Len(suite.T(), suite.caughtRequests, 1)
	authHeader1 := suite.caughtRequests[0].header["Authorization"]
	assert.Len(suite.T(), authHeader1, 1)
	assert.Equal(suite.T(), "Basic "+basicAuth, authHeader1[0])

	// Now the runToken should be set from the fetched run
	assert.NotNil(suite.T(), api.auth.runToken)
	assert.NotEmpty(suite.T(), *api.auth.runToken)

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
	assert.Nil(suite.T(), err)

	// Verify: Second request uses Bearer token
	assert.Len(suite.T(), suite.caughtRequests, 1)
	authHeader2 := suite.caughtRequests[0].header["Authorization"]
	assert.Len(suite.T(), authHeader2, 1)
	assert.Contains(suite.T(), authHeader2[0], "Bearer ")
	assert.NotEqual(suite.T(), "Basic "+basicAuth, authHeader2[0])

	// Execute: Clear the run token
	api.ClearRunToken()

	// Verify: runToken is now nil
	assert.Nil(suite.T(), api.auth.runToken)

	// Reset caught requests for next call
	suite.caughtRequests = make([]*CaughtRequest, 0)

	// Execute: Fetch next run (should use basic auth again)
	run2, err := api.FetchRunDetails("test-node-2")
	assert.Nil(suite.T(), err)
	assert.NotNil(suite.T(), run2)

	// Verify: Third request uses Basic auth again
	assert.Len(suite.T(), suite.caughtRequests, 1)
	authHeader3 := suite.caughtRequests[0].header["Authorization"]
	assert.Len(suite.T(), authHeader3, 1)
	assert.Equal(suite.T(), "Basic "+basicAuth, authHeader3[0])

	// Verify: runToken is set again from the new run
	assert.NotNil(suite.T(), api.auth.runToken)
	assert.NotEmpty(suite.T(), *api.auth.runToken)
}

func (suite *ApiTestSuite) Test_ClearRunToken_MultipleRunCycle() {
	// This test verifies the complete cycle: fetch -> use token -> clear -> fetch again
	// Simulating multiple runs being processed by a worker

	basicAuth := base64.StdEncoding.EncodeToString([]byte("worker-user:worker-pass"))
	wAuth := &runApiAuth{basic: "Basic " + basicAuth}
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
		assert.Nil(suite.T(), err, "Iteration %d: FetchRunDetails should not error", i)
		assert.NotNil(suite.T(), run, "Iteration %d: Run should not be nil", i)

		// Verify fetch used Basic auth
		assert.Len(suite.T(), suite.caughtRequests, 1, "Iteration %d: Should have one request", i)
		fetchAuthHeader := suite.caughtRequests[0].header["Authorization"]
		assert.Equal(suite.T(), "Basic "+basicAuth, fetchAuthHeader[0],
			"Iteration %d: Fetch should use Basic auth", i)

		// Verify runToken is set
		assert.NotNil(suite.T(), api.auth.runToken, "Iteration %d: runToken should be set after fetch", i)

		// Simulate run operations (Register, UpdateState) - should use Bearer token
		suite.caughtRequests = make([]*CaughtRequest, 0)

		status := &RunStatus{
			RunId:  run.Id,
			Status: IN_PROGRESS,
			Steps:  nil,
		}
		_, err = api.UpdateState(status)
		assert.Nil(suite.T(), err, "Iteration %d: UpdateState should not error", i)

		// Verify update used Bearer token
		updateAuthHeader := suite.caughtRequests[0].header["Authorization"]
		assert.Contains(suite.T(), updateAuthHeader[0], "Bearer ",
			"Iteration %d: Update should use Bearer token", i)

		// Clear token after run completes (simulating worker behavior)
		api.ClearRunToken()
		assert.Nil(suite.T(), api.auth.runToken,
			"Iteration %d: runToken should be nil after clearing", i)
	}
}
