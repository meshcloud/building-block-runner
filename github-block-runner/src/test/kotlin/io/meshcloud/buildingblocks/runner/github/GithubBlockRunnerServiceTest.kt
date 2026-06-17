package io.meshcloud.buildingblocks.runner.github

import io.meshcloud.buildingblocks.runner.meshobject.HalLink
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClient
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.buildingblocks.runner.security.DecryptionService
import io.meshcloud.meshobjects.objects.*
import io.mockk.every
import io.mockk.mockk
import io.mockk.slot
import io.mockk.verify
import org.assertj.core.api.Assertions.assertThat
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import java.util.*

class GithubBlockRunnerServiceTest {

  private lateinit var sut: GithubBlockRunnerService

  private val blockRunClientFetcherMockk: BlockRunClientFetcher = mockk()
  private val blockRunClientMockk: BlockRunClient = mockk()
  private val githubClientFactoryMock: GitHubClientFactory = mockk()
  private val githubClient: GithubClient = mockk()
  private val appTokenFactory: AppTokenFactory = mockk()
  private val decryptionServiceMockk: DecryptionService = mockk()
  private val clock: Clock = Clock.fixed(Instant.parse("2023-01-01T12:00:30Z"), ZoneOffset.UTC)

  @BeforeEach
  fun setup() {
    every { githubClientFactoryMock.provideClientFor(any()) } returns githubClient
    every { blockRunClientFetcherMockk.fetchBlockRunClient() } returns blockRunClientMockk
    every { blockRunClientMockk.activeBlockRun } returns ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGithubImplementation.test(),
    )
    every { blockRunClientMockk.registerAsSource(any(), any()) } returns Unit
    every { blockRunClientMockk.updateBlockRun(any()) } returns Unit
    every { decryptionServiceMockk.decrypt(any()) } answers { arg<String>(0) + "-decrypted" }
    every { decryptionServiceMockk.decryptBlockRunInputs(any()) } returnsArgument 0
    every { appTokenFactory.getAppAuthToken(any(), any()) } returns "appAuthToken"

    // Mock the new workflow polling methods to return empty results by default
    every { githubClient.listWorkflowRuns(any(), any(), any(), any(), any()) } returns emptyList()
    every { githubClient.getWorkflowRun(any(), any(), any(), any()) } returns mockk()

    sut = GithubBlockRunnerService(
      blockRunClientFetcher = blockRunClientFetcherMockk,
      gitHubClientFactory = githubClientFactoryMock,
      decryptionService = decryptionServiceMockk,
      appTokenFactory = appTokenFactory,
      clock = clock,
    )
  }

  private fun setupGitHubClientStubs() {
    every { githubClient.getInstallationAuthToken(any(), any()) } returns "installationAuthToken"
    every { githubClient.getInstallationId(any(), any(), any()) } returns "installationId"
    every {
      githubClient.triggerWorkflow(
        any(),
        any(),
        any(),
        any(),
        any(),
        any(),
      )
    } returns GithubClient.TriggerWorkflowResult.Success
  }

  private fun createAsyncRun(
    id: String = "test",
    implementation: MeshBuildingBlockGithubImplementation = MeshBuildingBlockGithubImplementation.test(async = true),
    inputs: List<MeshBuildingBlockRun.MeshBuildingBlockRunSpec.MeshBuildingBlockInputsForRun> = emptyList(),
  ): ProcessableBlockRun = ProcessableBlockRun.test(
    implementation = implementation,
    inputs = inputs,
    links = mapOf("self" to HalLink(href = "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns/$id")),
  )

  private fun createSyncRun(
    id: String = "test",
  ): ProcessableBlockRun = createAsyncRun(
    id = id,
    implementation = MeshBuildingBlockGithubImplementation.test(async = false),
  )

  private fun verifyGitHubWorkflowTrigger(
    installationAuthToken: String = "installationAuthToken",
    owner: String = "owner",
    repositoryName: String = "repository",
    workflowName: String = "provision.yml",
    recognizedUnsupportedInputs: Set<String> = setOf(
      "buildingBlockRun",
      "buildingBlockRunUrl",
      BuildingBlockWorkflowInputsBuilder.MESHSTACK_API_TOKEN_KEY,
      BuildingBlockWorkflowInputsBuilder.MESHSTACK_RUN_TOKEN_KEY,
    ),
  ): GithubClient.DispatchWorkflowPayload {
    val slot = slot<GithubClient.DispatchWorkflowPayload>()
    verify(exactly = 1) {
      githubClient.triggerWorkflow(
        installationAuthToken = eq(installationAuthToken),
        owner = eq(owner),
        repositoryName = eq(repositoryName),
        workflowName = workflowName,
        payload = capture(slot),
        recognizedUnsupportedInputs = recognizedUnsupportedInputs,
      )
    }

    return slot.captured
  }

  @Test
  fun `processBlock reports error when deserializing a GitHub response fails`() {
    val runWithLinks = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGitlabImplementation.test(),
    )

    every { blockRunClientFetcherMockk.fetchBlockRunClient() } returns blockRunClientMockk
    every { blockRunClientMockk.activeBlockRun } returns runWithLinks

    every {
      githubClient.getInstallationId(any(), any(), any())
    } throws Exception()

    val result = sut.processBlock()

    assertThat(result).isNull()

    verify(exactly = 1) { blockRunClientMockk.registerAsSource(any(), "Trigger GitHub Action") }
    verify(atLeast = 1) { blockRunClientMockk.updateBlockRun(any()) }
  }

  @Test
  fun `does nothing when there is no run available`() {
    every { blockRunClientFetcherMockk.fetchBlockRunClient() } returns null
    val result = sut.processBlock()
    assertThat(result).isNull()
    verify(exactly = 0) { blockRunClientMockk.registerAsSource(any(), any()) }
  }

  @Test
  fun `does nothing when there is an exception during run fetching`() {
    every { blockRunClientFetcherMockk.fetchBlockRunClient() } throws Exception()
    val result = sut.processBlock()
    assertThat(result).isNull()
    verify(exactly = 0) { blockRunClientMockk.registerAsSource(any(), any()) }
  }

  @Test
  fun `triggering works as expected`() {
    val runWithLinks = createAsyncRun()

    every { blockRunClientFetcherMockk.fetchBlockRunClient() } returns blockRunClientMockk
    every { blockRunClientMockk.activeBlockRun } returns runWithLinks
    every { decryptionServiceMockk.decrypt(any()) } returns "decryptedPem"
    setupGitHubClientStubs()

    val result = sut.processBlock()

    verify(exactly = 1) {
      blockRunClientMockk.registerAsSource(
        any(),
        "Trigger GitHub Action",
      )
    }
    verify(atLeast = 1) { blockRunClientMockk.updateBlockRun(any()) }
    verify(exactly = 1) { appTokenFactory.getAppAuthToken(any(), any()) }
    verify(exactly = 1) {
      githubClient.getInstallationId(
        appAuthToken = eq("appAuthToken"),
        owner = eq("owner"),
        repositoryName = eq("repository"),
      )
    }
    verify(exactly = 1) {
      githubClient.getInstallationAuthToken(
        appAuthToken = eq("appAuthToken"),
        installationId = "installationId",
      )
    }

    val sentPayload = verifyGitHubWorkflowTrigger()

    val expectedRunObject = """
      {
        "kind" : "meshBuildingBlockRun",
        "apiVersion" : "v1",
        "metadata" : {
          "uuid" : "test"
        },
        "spec" : {
          "runNumber" : 1,
          "buildingBlock" : {
            "uuid" : "test",
            "spec" : {
              "displayName" : "name",
              "workspaceIdentifier" : "workspace",
              "projectIdentifier" : "project",
              "fullPlatformIdentifier" : "platform",
              "inputs" : [ ],
              "parentBuildingBlocks" : [ ]
            }
          },
          "buildingBlockDefinition" : {
            "uuid" : "test",
            "spec" : {
              "workspaceIdentifier": "test-workspace",
              "version" : 1,
              "implementation" : {
                "type" : "GITHUB_WORKFLOW"
              }
            }
          },
          "behavior" : "APPLY",
          "runToken" : "test"
        },
        "status" : "IN_PROGRESS",
        "_links" : {
          "self" : {
            "href" : "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns/test",
            "templated" : null
          }
        }
      }
    """
    verifyExpectedRunObjectSent(expectedRunObject, sentPayload)

    assertThat(result).isNotNull
    assertThat(result!!.metadata.uuid).isEqualTo("test")
  }

  private fun verifyExpectedRunObjectSent(
    expectedRunObject: String,
    sentPayload: GithubClient.DispatchWorkflowPayload,
  ) {
    val expectedRunObjectCompactJson = expectedRunObject.replace("\\s".toRegex(), "") // remove all whitespace
    val encodedRun = sentPayload.inputs["buildingBlockRun"]
    val decodedRun = String(Base64.getDecoder().decode(encodedRun))

    assertThat(decodedRun).isEqualTo(expectedRunObjectCompactJson)
  }

  @Test
  fun `trigger is called with decrypted inputs and removed implementation details`() {
    val inputs = listOf(
      MeshBuildingBlockRun.MeshBuildingBlockRunSpec.MeshBuildingBlockInputsForRun(
        key = "test1",
        value = "1",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = false,
        isEnvironment = true,
      ),
      MeshBuildingBlockRun.MeshBuildingBlockRunSpec.MeshBuildingBlockInputsForRun(
        key = "test2",
        value = "2",
        type = MeshBuildingBlockIOType.STRING,
        isSensitive = true,
        isEnvironment = true,
      ),
      MeshBuildingBlockRun.MeshBuildingBlockRunSpec.MeshBuildingBlockInputsForRun(
        key = "test3",
        value = "3",
        type = MeshBuildingBlockIOType.FILE,
        isSensitive = true,
        isEnvironment = true,
      ),
      MeshBuildingBlockRun.MeshBuildingBlockRunSpec.MeshBuildingBlockInputsForRun(
        key = "test4",
        value = 4,
        type = MeshBuildingBlockIOType.INTEGER,
        isSensitive = true,
        isEnvironment = true,
      ),
    )
    val runWithLinks = createAsyncRun(inputs = inputs)

    every { blockRunClientFetcherMockk.fetchBlockRunClient() } returns blockRunClientMockk
    every { blockRunClientMockk.activeBlockRun } returns runWithLinks
    setupGitHubClientStubs()

    val result = sut.processBlock()

    val sentPayload = verifyGitHubWorkflowTrigger()
    val expectedRunObject = """
      {
        "kind" : "meshBuildingBlockRun",
        "apiVersion" : "v1",
        "metadata" : {
          "uuid" : "test"
        },
        "spec" : {
          "runNumber" : 1,
          "buildingBlock" : {
            "uuid" : "test",
            "spec" : {
              "displayName" : "name",
              "workspaceIdentifier" : "workspace",
              "projectIdentifier" : "project",
              "fullPlatformIdentifier" : "platform",
              "inputs" : [ 
                {
                  "key" : "test1",
                  "value" : "1",
                  "type" : "STRING",
                  "isSensitive" : false,
                  "isEnvironment" : true
                },
                {
                  "key" : "test2",
                  "value" : "2",
                  "type" : "STRING",
                  "isSensitive" : true,
                  "isEnvironment" : true
                },
                {
                  "key" : "test3",
                  "value" : "3",
                  "type" : "FILE",
                  "isSensitive" : true,
                  "isEnvironment" : true
                },
                {
                  "key" : "test4",
                  "value" : 4,
                  "type" : "INTEGER",
                  "isSensitive" : true,
                  "isEnvironment" : true
                }
              ],
              "parentBuildingBlocks" : [ ]
            }
          },
          "buildingBlockDefinition" : {
            "uuid" : "test",
            "spec" : {
              "workspaceIdentifier": "test-workspace",
              "version" : 1,
              "implementation" : {
                "type" : "GITHUB_WORKFLOW"
              }
            }
          },
          "behavior" : "APPLY",
          "runToken" : "test"
        },
        "status" : "IN_PROGRESS",
        "_links" : {
          "self" : {
            "href" : "https://meshstack.example.com/api/meshobjects/meshbuildingblockruns/test",
            "templated" : null
          }
        }
      }
    """
    verifyExpectedRunObjectSent(expectedRunObject, sentPayload)

    assertThat(result).isNotNull
    assertThat(result!!.metadata.uuid).isEqualTo("test")
    verify(exactly = 1) { decryptionServiceMockk.decryptBlockRunInputs(runWithLinks) }
  }

  @Test
  fun `processBlock handles async building blocks correctly`() {
    val runWithLinks = createAsyncRun()

    every { blockRunClientFetcherMockk.fetchBlockRunClient() } returns blockRunClientMockk
    every { blockRunClientMockk.activeBlockRun } returns runWithLinks
    every { decryptionServiceMockk.decrypt(any()) } returns "decryptedPem"
    setupGitHubClientStubs()

    val result = sut.processBlock()

    assertThat(result).isNotNull
    assertThat(result!!.metadata.uuid).isEqualTo("test")
    verify(exactly = 1) { githubClient.triggerWorkflow(any(), any(), any(), any(), any(), any()) }
    // Verify no polling methods were called for async mode
    verify(exactly = 0) { githubClient.listWorkflowRuns(any(), any(), any(), any(), any()) }
    verify(exactly = 0) { githubClient.getWorkflowRun(any(), any(), any(), any()) }
  }

  @Test
  fun `processBlock handles synchronous building blocks with workflow polling`() {
    val mockWorkflowRun = GithubClient.WorkflowRun(
      id = 123L,
      status = GithubClient.WorkflowRunStatus.COMPLETED,
      conclusion = "success",
      createdAt = "2023-01-01T12:00:01Z",
      updatedAt = "2023-01-01T12:05:00Z",
      htmlUrl = "https://github.com/owner/repo/actions/runs/123",
    )
    val runWithLinks = createSyncRun()

    every { blockRunClientFetcherMockk.fetchBlockRunClient() } returns blockRunClientMockk
    every { blockRunClientMockk.activeBlockRun } returns runWithLinks
    setupGitHubClientStubs()
    every { githubClient.getWorkflowRun(any(), any(), any(), any()) } returns mockWorkflowRun
    every { githubClient.listWorkflowRuns(any(), any(), any(), any(), any()) } returns listOf(mockWorkflowRun)
    every { githubClient.listWorkflowJobs(any(), any(), any(), any()) } returns emptyList()

    val result = sut.processBlock()

    assertThat(result).isNotNull
    assertThat(result!!.metadata.uuid).isEqualTo("test")
    verify(exactly = 1) { githubClient.triggerWorkflow(any(), any(), any(), any(), any(), any()) }
    verify(atLeast = 1) { githubClient.listWorkflowRuns(any(), any(), any(), any(), any()) }
    verify(exactly = 0) { githubClient.getWorkflowRun(any(), any(), any(), any()) }
  }

  @Test
  fun `processBlock handles UnsupportedInput result for buildingBlockRunUrl`() {
    val runWithLinks = createAsyncRun(
      implementation = MeshBuildingBlockGithubImplementation.test(async = true, omitRunObjectInput = true),
    )

    every { blockRunClientFetcherMockk.fetchBlockRunClient() } returns blockRunClientMockk
    every { blockRunClientMockk.activeBlockRun } returns runWithLinks
    setupGitHubClientStubs()
    every {
      githubClient.triggerWorkflow(
        any(),
        any(),
        any(),
        any(),
        any(),
        any(),
      )
    } returns GithubClient.TriggerWorkflowResult.UnsupportedInput(
      unsupportedInputNames = setOf("buildingBlockRunUrl"),
      responseBody = "Unexpected inputs provided: buildingBlockRunUrl",
    )

    val result = sut.processBlock()

    assertThat(result).isNull()

    val updateSlot = slot<MeshBuildingBlockRun.SourceUpdate>()
    verify(atLeast = 1) {
      blockRunClientMockk.updateBlockRun(
        capture(updateSlot),
      )
    }

    val capturedUpdate = updateSlot.captured
    assertThat(capturedUpdate.status).isEqualTo(MeshBuildingBlockRun.ExecutionStatus.FAILED)
    assertThat(capturedUpdate.steps?.first()?.systemMessage).contains("buildingBlockRunUrl")
    assertThat(capturedUpdate.steps?.first()?.systemMessage).contains("Pass only API URL")
  }

  @Test
  fun `processBlock handles UnsupportedInput result for buildingBlockRun`() {
    val runWithLinks = createAsyncRun(
      implementation = MeshBuildingBlockGithubImplementation.test(async = true, omitRunObjectInput = false),
    )

    every { blockRunClientFetcherMockk.fetchBlockRunClient() } returns blockRunClientMockk
    every { blockRunClientMockk.activeBlockRun } returns runWithLinks
    setupGitHubClientStubs()
    every {
      githubClient.triggerWorkflow(
        any(),
        any(),
        any(),
        any(),
        any(),
        any(),
      )
    } returns GithubClient.TriggerWorkflowResult.UnsupportedInput(
      unsupportedInputNames = setOf("buildingBlockRun"),
      responseBody = "Unexpected inputs provided: buildingBlockRun",
    )

    val result = sut.processBlock()

    assertThat(result).isNull()

    val updateSlot = slot<MeshBuildingBlockRun.SourceUpdate>()
    verify(atLeast = 1) {
      blockRunClientMockk.updateBlockRun(
        capture(updateSlot),
      )
    }

    val capturedUpdate = updateSlot.captured
    assertThat(capturedUpdate.status).isEqualTo(MeshBuildingBlockRun.ExecutionStatus.FAILED)
    assertThat(capturedUpdate.steps?.first()?.systemMessage).contains("buildingBlockRun")
  }

  @Test
  fun `processBlock handles Error result from GitHub API`() {
    val runWithLinks = createAsyncRun()

    every { blockRunClientFetcherMockk.fetchBlockRunClient() } returns blockRunClientMockk
    every { blockRunClientMockk.activeBlockRun } returns runWithLinks
    setupGitHubClientStubs()
    every {
      githubClient.triggerWorkflow(
        any(),
        any(),
        any(),
        any(),
        any(),
        any(),
      )
    } returns GithubClient.TriggerWorkflowResult.Error(
      statusCode = 404,
      responseBody = """{"message":"Not Found"}""",
    )

    val result = sut.processBlock()

    assertThat(result).isNull()

    val updateSlot = slot<MeshBuildingBlockRun.SourceUpdate>()
    verify(atLeast = 1) {
      blockRunClientMockk.updateBlockRun(
        capture(updateSlot),
      )
    }

    val capturedUpdate = updateSlot.captured
    assertThat(capturedUpdate.status).isEqualTo(MeshBuildingBlockRun.ExecutionStatus.FAILED)
    assertThat(capturedUpdate.steps?.first()?.systemMessage).contains("404")
    assertThat(capturedUpdate.steps?.first()?.systemMessage).contains("Not Found")
  }
}
