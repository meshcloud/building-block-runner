package tfrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_EndpointGeneration_CustomPredicate(t *testing.T) {
	// Setup: Test endpoint URL generation with custom predicate
	originalConfig := AppConfig
	defer func() { AppConfig = originalConfig }()

	myRunnerUuid := "myRunnerUuid"
	AppConfig = TfRunnerConfig{
		RunnerUuid: myRunnerUuid,
	}

	// Test the endpoint generation directly
	baseUrl := "http://test.example.com"
	requester := "test-requester"
	expectedUrl := "http://test.example.com/api/meshobjects/meshbuildingblockruns/create?forRunnerUuid=myRunnerUuid"

	actualUrl := getRunEndpoint(baseUrl, requester)
	assert.Equal(t, expectedUrl, actualUrl)
}

func Test_EndpointGeneration_DefaultSelector(t *testing.T) {
	// Setup: Test endpoint URL generation with valid RunnerUuid (always uses new endpoint now)
	originalConfig := AppConfig
	defer func() { AppConfig = originalConfig }()

	myRunnerUuid := "c3d4e5f6-a7b8-49c0-ad1e-2f3a4b5c6d7e"
	AppConfig = TfRunnerConfig{
		RunnerUuid: myRunnerUuid,
	}

	// Test the endpoint generation directly
	baseUrl := "http://test.example.com"
	requester := "test-requester"
	expectedUrl := "http://test.example.com/api/meshobjects/meshbuildingblockruns/create?forRunnerUuid=c3d4e5f6-a7b8-49c0-ad1e-2f3a4b5c6d7e"

	actualUrl := getRunEndpoint(baseUrl, requester)
	assert.Equal(t, expectedUrl, actualUrl)
}

func Test_EndpointGeneration_DifferentRunnerUuid(t *testing.T) {
	// Setup: Test endpoint URL generation with different RunnerUuid
	originalConfig := AppConfig
	defer func() { AppConfig = originalConfig }()

	myRunnerUuid := "d4e5f6a7-b8c9-40d1-be2f-3a4b5c6d7e8f"
	AppConfig = TfRunnerConfig{
		RunnerUuid: myRunnerUuid,
	}

	// Test the endpoint generation directly
	baseUrl := "http://test.example.com"
	requester := "test-requester"
	expectedUrl := "http://test.example.com/api/meshobjects/meshbuildingblockruns/create?forRunnerUuid=d4e5f6a7-b8c9-40d1-be2f-3a4b5c6d7e8f"

	actualUrl := getRunEndpoint(baseUrl, requester)
	assert.Equal(t, expectedUrl, actualUrl)
}
