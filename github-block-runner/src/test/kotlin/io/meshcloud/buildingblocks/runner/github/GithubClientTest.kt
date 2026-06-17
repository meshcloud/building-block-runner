package io.meshcloud.buildingblocks.runner.github

import com.github.tomakehurst.wiremock.client.WireMock.*
import io.meshcloud.buildingblocks.runner.MeshException
import io.meshcloud.buildingblocks.runner.github.GithubClient.WorkflowJobStatus
import io.meshcloud.buildingblocks.runner.github.GithubClient.WorkflowRunStatus
import io.meshcloud.buildingblocks.runner.meshobject.HalLink
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.objects.MeshBuildingBlockGithubImplementation
import org.assertj.core.api.Assertions.assertThat
import org.junit.jupiter.api.Assertions
import org.junit.jupiter.api.Test

class GithubClientTest : WiremockTestBase() {

  @Test
  fun triggerWorkflowWithRunObject() {
    val sut = GithubClient(BASE_URL)
    val b64run = """
        eyJraW5kIjoibWVzaEJ1aWxkaW5nQmxvY2tSdW4iLCJhcGlWZXJzaW9uIjoidjEiLCJtZXRhZGF0YSI6ey
        J1dWlkIjoidGVzdCJ9LCJzcGVjIjp7InJ1bk51bWJlciI6MSwiYnVpbGRpbmdCbG9jayI6eyJ1dWlkIjoi
        dGVzdCIsInNwZWMiOnsiZGlzcGxheU5hbWUiOiJuYW1lIiwid29ya3NwYWNlSWRlbnRpZmllciI6Indvcm
        tzcGFjZSIsInByb2plY3RJZGVudGlmaWVyIjoicHJvamVjdCIsImZ1bGxQbGF0Zm9ybUlkZW50aWZpZXIi
        OiJwbGF0Zm9ybSIsImlucHV0cyI6W10sInBhcmVudEJ1aWxkaW5nQmxvY2tzIjpbXX19LCJidWlsZGluZ0
        Jsb2NrRGVmaW5pdGlvbiI6eyJ1dWlkIjoidGVzdCIsInNwZWMiOnsid29ya3NwYWNlSWRlbnRpZmllciI6
        InRlc3Qtd29ya3NwYWNlIiwidmVyc2lvbiI6MSwiaW1wbGVtZW50YXRpb24iOnsidHlwZSI6IkdJVEhVQl
        9XT1JLRkxPVyJ9fX0sImJlaGF2aW9yIjoiQVBQTFkiLCJydW5Ub2tlbiI6InRlc3QifSwic3RhdHVzIjoi
        SU5fUFJPR1JFU1MiLCJfbGlua3MiOnsic2VsZiI6eyJocmVmIjoiaHR0cHM6Ly9tZXNoc3RhY2suZXhhbX
        BsZS5jb20vYXBpL21lc2hvYmplY3RzL21lc2hidWlsZGluZ2Jsb2NrcnVucy90ZXN0IiwidGVtcGxhdGVk
        IjpudWxsfX19
    """.trimIndent().replace("\n", "")

    val expectedJson = """{ 
      "ref": "ref", 
      "inputs": { 
        "buildingBlockRun": "$b64run" 
      }
    }
    """.trimIndent()

    stubWorkflowDispatchCall(expectedJson)

    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      links = mapOf("self" to HalLink(href = "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns/test")),
    )

    val payload = GithubClient.DispatchWorkflowPayload(
      ref = "ref",
      inputs = BuildingBlockWorkflowInputsBuilder.WithRun(run).toInputMap(),
    )

    val result = sut.triggerWorkflow(
      installationAuthToken = "token",
      owner = "owner",
      repositoryName = "repo",
      workflowName = "workflow.yml",
      payload = payload,
    )

    assertThat(result).isEqualTo(GithubClient.TriggerWorkflowResult.Success)
  }

  @Test
  fun triggerWorkflowWithRunUrl() {
    val sut = GithubClient(BASE_URL)

    val expectedJson = """{
      "ref": "ref",
      "inputs": {
        "buildingBlockRunUrl": "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns/test"
      }
    }
    """.trimIndent()

    stubWorkflowDispatchCall(expectedJson)

    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      links = mapOf("self" to HalLink(href = "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns/test")),
    )

    val payload = GithubClient.DispatchWorkflowPayload(
      ref = "ref",
      inputs = BuildingBlockWorkflowInputsBuilder.WithUrl(run).toInputMap(),
    )

    val result = sut.triggerWorkflow(
      installationAuthToken = "token",
      owner = "owner",
      repositoryName = "repo",
      workflowName = "workflow.yml",
      payload = payload,
    )

    assertThat(result).isEqualTo(GithubClient.TriggerWorkflowResult.Success)
  }

  private fun stubWorkflowDispatchCall(expectedJson: String) {
    wireMockServer.stubFor(
      post(urlEqualTo("/repos/owner/repo/actions/workflows/workflow.yml/dispatches"))
        .withHeader("content-type", equalTo("application/json; charset=UTF-8"))
        .withHeader("accept", equalTo("application/vnd.github+json"))
        .withHeader("X-GitHub-Api-Version", equalTo("2022-11-28"))
        .withHeader("Authorization", equalTo("Bearer token"))
        .withRequestBody(equalToJson(expectedJson))
        .willReturn(
          noContent(),
        ),
    )
  }

  @Test
  fun checksForMissingActionsPermissions() {
    val sut = GithubClient(BASE_URL)

    wireMockServer.stubFor(
      post(urlEqualTo("/app/installations/123/access_tokens"))
        .withHeader("accept", equalTo("application/vnd.github+json"))
        .withHeader("X-GitHub-Api-Version", equalTo("2022-11-28"))
        .willReturn(
          created().withBody(
            """
                    {
                      "token": "redacted",
                      "expires_at": "2025-05-27T09:41:14Z",
                      "permissions": {"metadata":"read"},
                      "repository_selection":"all"
                    }
            """.trimIndent(),
          ),
        ),
    )

    val exception = Assertions.assertThrows(MeshException::class.java) {
      sut.getInstallationAuthToken(
        appAuthToken = "token",
        installationId = "123",
      )
    }

    Assertions.assertTrue(exception.message?.contains("Required permissions: actions=write") ?: false)
  }

  @Test
  fun getsWorkflowRunById() {
    val sut = GithubClient(BASE_URL)

    wireMockServer.stubFor(
      get(urlEqualTo("/repos/owner/repo/actions/runs/123456789"))
        .withHeader("accept", equalTo("application/vnd.github+json"))
        .withHeader("X-GitHub-Api-Version", equalTo("2022-11-28"))
        .withHeader("Authorization", equalTo("Bearer token"))
        .willReturn(
          ok().withBody(
            """
                    {
                      "id": 123456789,
                      "status": "completed",
                      "conclusion": "success",
                      "created_at": "2023-01-01T12:00:00Z",
                      "updated_at": "2023-01-01T12:05:00Z",
                      "html_url": "https://github.com/owner/repo/actions/runs/123456789"
                    }
            """.trimIndent(),
          ),
        ),
    )

    val run = sut.getWorkflowRun(
      installationAuthToken = "token",
      owner = "owner",
      repositoryName = "repo",
      runId = 123456789,
    )

    assertThat(run.id).isEqualTo(123456789)
    assertThat(run.status).isEqualTo(WorkflowRunStatus.COMPLETED)
    assertThat(run.conclusion).isEqualTo("success")
    assertThat(run.htmlUrl).isEqualTo("https://github.com/owner/repo/actions/runs/123456789")
  }

  @Test
  fun listsWorkflowJobs() {
    val sut = GithubClient(BASE_URL)

    wireMockServer.stubFor(
      get(urlEqualTo("/repos/owner/repo/actions/runs/123456789/jobs"))
        .withHeader("accept", equalTo("application/vnd.github+json"))
        .withHeader("X-GitHub-Api-Version", equalTo("2022-11-28"))
        .withHeader("Authorization", equalTo("Bearer token"))
        .willReturn(
          ok().withBody(
            """
                    {
                      "total_count": 2,
                      "jobs": [
                        {
                          "id": 11111,
                          "name": "build",
                          "status": "completed",
                          "conclusion": "success",
                          "started_at": "2023-01-01T12:01:00Z",
                          "completed_at": "2023-01-01T12:03:00Z",
                          "html_url": "https://github.com/owner/repo/actions/runs/123456789/job/11111"
                        },
                        {
                          "id": 22222,
                          "name": "test",
                          "status": "in_progress",
                          "conclusion": null,
                          "started_at": "2023-01-01T12:02:00Z",
                          "completed_at": null,
                          "html_url": "https://github.com/owner/repo/actions/runs/123456789/job/22222"
                        }
                      ]
                    }
            """.trimIndent(),
          ),
        ),
    )

    val jobs = sut.listWorkflowJobs(
      installationAuthToken = "token",
      owner = "owner",
      repositoryName = "repo",
      runId = 123456789,
    )

    assertThat(jobs).hasSize(2)
    assertThat(jobs[0].id).isEqualTo(11111)
    assertThat(jobs[0].name).isEqualTo("build")
    assertThat(jobs[0].status).isEqualTo(WorkflowJobStatus.COMPLETED)
    assertThat(jobs[0].conclusion).isEqualTo("success")
    assertThat(jobs[0].startedAt).isEqualTo("2023-01-01T12:01:00Z")
    assertThat(jobs[0].completedAt).isEqualTo("2023-01-01T12:03:00Z")
    assertThat(jobs[1].id).isEqualTo(22222)
    assertThat(jobs[1].name).isEqualTo("test")
    assertThat(jobs[1].status).isEqualTo(WorkflowJobStatus.IN_PROGRESS)
    assertThat(jobs[1].conclusion).isNull()
    assertThat(jobs[1].completedAt).isNull()
  }

  @Test
  fun listsWorkflowRuns() {
    val sut = GithubClient(BASE_URL)

    wireMockServer.stubFor(
      get(urlPathEqualTo("/repos/owner/repo/actions/workflows/workflow.yml/runs"))
        .withQueryParam("per_page", equalTo("5"))
        .withHeader("accept", equalTo("application/vnd.github+json"))
        .withHeader("X-GitHub-Api-Version", equalTo("2022-11-28"))
        .withHeader("Authorization", equalTo("Bearer token"))
        .willReturn(
          ok().withBody(
            """
                    {
                      "total_count": 2,
                      "workflow_runs": [
                        {
                          "id": 123456789,
                          "status": "completed",
                          "conclusion": "success",
                          "created_at": "2023-01-01T12:00:00Z",
                          "updated_at": "2023-01-01T12:05:00Z",
                          "html_url": "https://github.com/owner/repo/actions/runs/123456789"
                        },
                        {
                          "id": 987654321,
                          "status": "in_progress",
                          "conclusion": null,
                          "created_at": "2023-01-01T11:00:00Z",
                          "updated_at": "2023-01-01T11:05:00Z",
                          "html_url": "https://github.com/owner/repo/actions/runs/987654321"
                        }
                      ]
                    }
            """.trimIndent(),
          ),
        ),
    )

    val runs = sut.listWorkflowRuns(
      installationAuthToken = "token",
      owner = "owner",
      repositoryName = "repo",
      workflowName = "workflow.yml",
      perPage = 5,
    )

    assertThat(runs).hasSize(2)
    assertThat(runs[0].id).isEqualTo(123456789)
    assertThat(runs[0].status).isEqualTo(WorkflowRunStatus.COMPLETED)
    assertThat(runs[0].conclusion).isEqualTo("success")
    assertThat(runs[0].createdAt).isEqualTo("2023-01-01T12:00:00Z")
    assertThat(runs[0].htmlUrl).isEqualTo("https://github.com/owner/repo/actions/runs/123456789")
    assertThat(runs[1].id).isEqualTo(987654321)
    assertThat(runs[1].status).isEqualTo(WorkflowRunStatus.IN_PROGRESS)
    assertThat(runs[1].conclusion).isNull()
  }

  @Test
  fun `triggerWorkflow returns UnsupportedInput when buildingBlockRunUrl is not supported`() {
    val sut = GithubClient(BASE_URL)

    wireMockServer.stubFor(
      post(urlEqualTo("/repos/owner/repo/actions/workflows/workflow.yml/dispatches"))
        .willReturn(
          status(422).withBody(
            """
                    {
                      "message": "Unexpected inputs provided",
                      "errors": ["buildingBlockRunUrl"]
                    }
            """.trimIndent(),
          ),
        ),
    )

    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      links = mapOf("self" to HalLink(href = "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns/test")),
    )
    val payload = GithubClient.DispatchWorkflowPayload(
      ref = "main",
      inputs = BuildingBlockWorkflowInputsBuilder.WithUrl(run).toInputMap(),
    )

    val result = sut.triggerWorkflow(
      installationAuthToken = "token",
      owner = "owner",
      repositoryName = "repo",
      workflowName = "workflow.yml",
      payload = payload,
      recognizedUnsupportedInputs = setOf("buildingBlockRunUrl"),
    )

    assertThat(result).isInstanceOf(GithubClient.TriggerWorkflowResult.UnsupportedInput::class.java)
    val unsupportedInput = result as GithubClient.TriggerWorkflowResult.UnsupportedInput
    assertThat(unsupportedInput.unsupportedInputNames).containsExactly("buildingBlockRunUrl")
    assertThat(unsupportedInput.responseBody).contains("Unexpected inputs provided")
  }

  @Test
  fun `triggerWorkflow returns UnsupportedInput when buildingBlockRun is not supported`() {
    val sut = GithubClient(BASE_URL)

    wireMockServer.stubFor(
      post(urlEqualTo("/repos/owner/repo/actions/workflows/workflow.yml/dispatches"))
        .willReturn(
          status(422).withBody(
            """
                    {
                      "message": "Unexpected inputs provided",
                      "errors": ["buildingBlockRun"]
                    }
            """.trimIndent(),
          ),
        ),
    )

    val run = ProcessableBlockRun.test(implementation = MeshBuildingBlockGithubImplementation.test())
    val payload = GithubClient.DispatchWorkflowPayload(
      ref = "main",
      inputs = BuildingBlockWorkflowInputsBuilder.WithRun(
        buildingBlockRun = run,
      ).toInputMap(),
    )

    val result = sut.triggerWorkflow(
      installationAuthToken = "token",
      owner = "owner",
      repositoryName = "repo",
      workflowName = "workflow.yml",
      payload = payload,
      recognizedUnsupportedInputs = setOf("buildingBlockRun"),
    )

    assertThat(result).isInstanceOf(GithubClient.TriggerWorkflowResult.UnsupportedInput::class.java)
    val unsupportedInput = result as GithubClient.TriggerWorkflowResult.UnsupportedInput
    assertThat(unsupportedInput.unsupportedInputNames).containsExactly("buildingBlockRun")
  }

  @Test
  fun `triggerWorkflow returns Error for unprocessable entity without specific input error`() {
    val sut = GithubClient(BASE_URL)

    wireMockServer.stubFor(
      post(urlEqualTo("/repos/owner/repo/actions/workflows/workflow.yml/dispatches"))
        .willReturn(
          status(422).withBody(
            """
                    {
                      "message": "Workflow file not found"
                    }
            """.trimIndent(),
          ),
        ),
    )

    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      links = mapOf("self" to HalLink(href = "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns/test")),
    )
    val payload = GithubClient.DispatchWorkflowPayload(
      ref = "main",
      inputs = BuildingBlockWorkflowInputsBuilder.WithUrl(run).toInputMap(),
    )

    val result = sut.triggerWorkflow(
      installationAuthToken = "token",
      owner = "owner",
      repositoryName = "repo",
      workflowName = "workflow.yml",
      payload = payload,
      recognizedUnsupportedInputs = emptySet(),
    )

    assertThat(result).isInstanceOf(GithubClient.TriggerWorkflowResult.Error::class.java)
    val error = result as GithubClient.TriggerWorkflowResult.Error
    assertThat(error.statusCode).isEqualTo(422)
    assertThat(error.responseBody).contains("Workflow file not found")
  }

  @Test
  fun `triggerWorkflow returns Error for server errors`() {
    val sut = GithubClient(BASE_URL)

    wireMockServer.stubFor(
      post(urlEqualTo("/repos/owner/repo/actions/workflows/workflow.yml/dispatches"))
        .willReturn(
          status(500).withBody(
            """
                    {
                      "message": "Internal server error"
                    }
            """.trimIndent(),
          ),
        ),
    )

    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      links = mapOf("self" to HalLink(href = "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns/test")),
    )
    val payload = GithubClient.DispatchWorkflowPayload(
      ref = "main",
      inputs = BuildingBlockWorkflowInputsBuilder.WithUrl(run).toInputMap(),
    )

    val result = sut.triggerWorkflow(
      installationAuthToken = "token",
      owner = "owner",
      repositoryName = "repo",
      workflowName = "workflow.yml",
      payload = payload,
    )

    assertThat(result).isInstanceOf(GithubClient.TriggerWorkflowResult.Error::class.java)
    val error = result as GithubClient.TriggerWorkflowResult.Error
    assertThat(error.statusCode).isEqualTo(500)
    assertThat(error.responseBody).contains("Internal server error")
  }

  @Test
  fun `triggerWorkflow returns Error for unauthorized requests`() {
    val sut = GithubClient(BASE_URL)

    wireMockServer.stubFor(
      post(urlEqualTo("/repos/owner/repo/actions/workflows/workflow.yml/dispatches"))
        .willReturn(
          status(401).withBody(
            """
                    {
                      "message": "Bad credentials"
                    }
            """.trimIndent(),
          ),
        ),
    )

    val run = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
      links = mapOf("self" to HalLink(href = "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns/test")),
    )
    val payload = GithubClient.DispatchWorkflowPayload(
      ref = "main",
      inputs = BuildingBlockWorkflowInputsBuilder.WithUrl(run).toInputMap(),
    )

    val result = sut.triggerWorkflow(
      installationAuthToken = "invalid-token",
      owner = "owner",
      repositoryName = "repo",
      workflowName = "workflow.yml",
      payload = payload,
    )

    assertThat(result).isInstanceOf(GithubClient.TriggerWorkflowResult.Error::class.java)
    val error = result as GithubClient.TriggerWorkflowResult.Error
    assertThat(error.statusCode).isEqualTo(401)
    assertThat(error.responseBody).contains("Bad credentials")
  }
}
