package io.meshcloud.buildingblocks.runner.gitlab

import com.github.tomakehurst.wiremock.client.WireMock.*
import io.meshcloud.buildingblocks.runner.meshobject.HalLink
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.objects.MeshBuildingBlockGitlabImplementation
import io.meshcloud.meshobjects.objects.MeshBuildingBlockIOType
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import org.junit.jupiter.api.Test

class GitLabClientTest : WiremockTestBase() {

  @Test
  fun `triggerPipeline triggers pipeline`() {
    wireMockServer.stubFor(
      post(urlPathEqualTo("/api/v4/projects/1111111/trigger/pipeline"))
        .withMultipartRequestBody(
          aMultipart()
            .withName("token")
            .withBody(equalTo("TOKEN"))
        )
        .withMultipartRequestBody(
          aMultipart()
            .withName("ref")
            .withBody(equalTo("refName"))
        )
        .withMultipartRequestBody(
          aMultipart()
            .withName("variables[MESHSTACK_BEHAVIOR]")
            .withBody(equalTo("APPLY"))
        )
        .withMultipartRequestBody(
          aMultipart()
            .withName("variables[MESHSTACK_RUN]")
            .withBody(matching(".*")) // Matches any value
        )
        .withMultipartRequestBody(
          aMultipart()
            .withName("variables[envInput]")
            .withBody(equalTo("testEnv"))
        )
        .withMultipartRequestBody(
          aMultipart()
            .withName("inputs[inputInput]")
            .withBody(equalTo("testInput"))
        )
        .withMultipartRequestBody(
          aMultipart()
            .withName("variables[MESHSTACK_SELF_URL]")
            .withBody(equalTo("http://localhost:8080/api/meshobjects/meshbuildingblockruns/1730033c-f8dd-44d0-8956-e09d30b3bd17"))
        )
        .withMultipartRequestBody(
          aMultipart()
            .withName("variables[MESHSTACK_REGISTER_SOURCE_URL]")
            .withBody(equalTo("http://localhost:8080/api/meshobjects/meshbuildingblockruns/1730033c-f8dd-44d0-8956-e09d30b3bd17/status/source"))
        )
        .withMultipartRequestBody(
          aMultipart()
            .withName("variables[MESHSTACK_UPDATE_SOURCE_URL]")
            .withBody(equalTo("http://localhost:8080/api/meshobjects/meshbuildingblockruns/1730033c-f8dd-44d0-8956-e09d30b3bd17/status/source/{sourceId}"))
        )
        .withMultipartRequestBody(
          aMultipart()
            .withName("variables[MESHSTACK_BASE_URL]")
            .withBody(equalTo("http://localhost:8080"))
        )
        .willReturn(
          aResponse()
            .withStatus(200)
        )
    )

    val sut = GitLabClient(BASE_URL)

    sut.triggerPipeline(
      pipelineToken = "TOKEN",
      refName = "refName",
      projectId = "1111111",
      run = ProcessableBlockRun.test(
        implementation = MeshBuildingBlockGitlabImplementation.test(),
        inputs = listOf(
          MeshBuildingBlockRun.MeshBuildingBlockRunSpec.MeshBuildingBlockInputsForRun(
            key = "envInput",
            value = "testEnv",
            type = MeshBuildingBlockIOType.STRING,
            isSensitive = false,
            isEnvironment = true
          ),
          MeshBuildingBlockRun.MeshBuildingBlockRunSpec.MeshBuildingBlockInputsForRun(
            key = "inputInput",
            value = "testInput",
            type = MeshBuildingBlockIOType.STRING,
            isSensitive = false,
            isEnvironment = false
          )
        ),
        links = mapOf(
          "self" to HalLink("http://localhost:8080/api/meshobjects/meshbuildingblockruns/1730033c-f8dd-44d0-8956-e09d30b3bd17"),
          "registerSource" to HalLink("http://localhost:8080/api/meshobjects/meshbuildingblockruns/1730033c-f8dd-44d0-8956-e09d30b3bd17/status/source"),
          "updateSource" to HalLink("http://localhost:8080/api/meshobjects/meshbuildingblockruns/1730033c-f8dd-44d0-8956-e09d30b3bd17/status/source/{sourceId}", true),
          "meshstackBaseUrl" to HalLink("http://localhost:8080"),
        )
      )
    )
  }
}
