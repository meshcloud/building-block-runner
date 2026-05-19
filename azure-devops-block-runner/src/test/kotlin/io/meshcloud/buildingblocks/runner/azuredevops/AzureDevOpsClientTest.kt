package io.meshcloud.buildingblocks.runner.azuredevops

import com.github.tomakehurst.wiremock.client.WireMock.*
import io.meshcloud.buildingblocks.runner.azuredevops.client.AzureDevOpsClient
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.objects.MeshBuildingBlockAzureDevOpsImplementation
import org.assertj.core.api.Assertions.assertThat
import org.junit.jupiter.api.AfterEach
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test

class AzureDevOpsClientTest : WiremockTestBase() {

  private lateinit var client: AzureDevOpsClient
  private val dummyRun = ProcessableBlockRun.test(MeshBuildingBlockAzureDevOpsImplementation.test())

  @BeforeEach
  fun setup() {
    val wireMockBaseUrl = "http://localhost:${wireMockServer.port()}"
    client = AzureDevOpsClient(
      azureDevOpsBaseUrl = wireMockBaseUrl,
      accessToken = "token",
      organization = "org",
      project = "proj",
      pipelineId = "1",
      run = dummyRun
    )
  }

  @AfterEach
  fun resetWireMock() {
    wireMockServer.resetAll()
  }

  @Test
  fun `triggerPipeline sends correct POST request`() {
    val jsonResponse = """
      {
        "id": 123,
        "name": "test",
        "createdDate": "2024-01-01T00:00:00Z",
        "finishedDate": null,
        "state": "inProgress",
        "result": "succeeded",
        "url": "someurl",
        "links": {}
      }
    """.trimIndent()
    wireMockServer.stubFor(post(urlPathEqualTo("/org/proj/_apis/pipelines/1/runs"))
      .willReturn(aResponse().withStatus(200).withBody(jsonResponse)))

    val result = client.triggerPipeline()
    assertThat(result.id).isEqualTo(123L)
    wireMockServer.verify(
      postRequestedFor(urlPathEqualTo("/org/proj/_apis/pipelines/1/runs"))
        .withRequestBody(notContaining("\"resources\""))
    )
  }

  @Test
  fun `triggerPipeline includes resources refName when refName is set`() {
    val jsonResponse = """
      {
        "id": 456,
        "name": "test",
        "createdDate": "2024-01-01T00:00:00Z",
        "finishedDate": null,
        "state": "inProgress",
        "result": "succeeded",
        "url": "someurl",
        "links": {}
      }
    """.trimIndent()
    wireMockServer.stubFor(post(urlPathEqualTo("/org/proj/_apis/pipelines/1/runs"))
      .willReturn(aResponse().withStatus(200).withBody(jsonResponse)))

    val wireMockBaseUrl = "http://localhost:${wireMockServer.port()}"
    val clientWithRef = AzureDevOpsClient(
      azureDevOpsBaseUrl = wireMockBaseUrl,
      accessToken = "token",
      organization = "org",
      project = "proj",
      pipelineId = "1",
      run = dummyRun,
      refName = "refs/heads/feature/my-branch"
    )

    val result = clientWithRef.triggerPipeline()
    assertThat(result.id).isEqualTo(456L)
    wireMockServer.verify(
      postRequestedFor(urlPathEqualTo("/org/proj/_apis/pipelines/1/runs"))
        .withRequestBody(matchingJsonPath("$.resources.repositories.self.refName", equalTo("refs/heads/feature/my-branch")))
    )
  }

  @Test
  fun `getPipelineRun sends correct GET request`() {
    val jsonResponse = """
      {
        "id": 123,
        "name": "test",
        "createdDate": "2024-01-01T00:00:00Z",
        "finishedDate": null,
        "state": "inProgress",
        "result": "succeeded",
        "url": "someurl",
        "links": {}
      }
    """.trimIndent()
    wireMockServer.stubFor(get(urlPathEqualTo("/org/proj/_apis/pipelines/1/runs/123"))
      .willReturn(aResponse().withStatus(200).withBody(jsonResponse)))

    val result = client.getPipelineRun(123L)
    assertThat(result.id).isEqualTo(123L)
    wireMockServer.verify(getRequestedFor(urlPathEqualTo("/org/proj/_apis/pipelines/1/runs/123")))
  }

  @Test
  fun `getPipelineTimeline sends correct GET request`() {
    val jsonResponse = """
      {
        "records": [
          {
            "id": "1",
            "name": "test",
            "type": "Stage",
            "state": null,
            "result": null,
            "startTime": null,
            "finishTime": null,
            "parentId": null,
            "order": 1
          }
        ]
      }
    """.trimIndent()
    wireMockServer.stubFor(get(urlPathEqualTo("/org/proj/_apis/build/builds/123/timeline"))
      .willReturn(aResponse().withStatus(200).withBody(jsonResponse)))

    val result = client.getPipelineTimeline(123L)
    assertThat(result).hasSize(1)
    wireMockServer.verify(getRequestedFor(urlPathEqualTo("/org/proj/_apis/build/builds/123/timeline")))
  }
}
