package io.meshcloud.buildingblocks.runner.gitlab

import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClient
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.buildingblocks.runner.security.DecryptionService
import io.meshcloud.meshobjects.objects.MeshBuildingBlockGitlabImplementation
import io.meshcloud.meshobjects.objects.MeshBuildingBlockIOType
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import io.mockk.every
import io.mockk.mockk
import io.mockk.verify
import org.assertj.core.api.Assertions.assertThat
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test

class GitLabBlockRunnerServiceTest {

  private lateinit var sut: GitLabBlockRunnerService

  private val gitLabClientFactoryMock: GitLabClientFactory = mockk()
  private val gitlabClient: GitLabClient = mockk()
  private val blockRunClientFetcherMock: BlockRunClientFetcher = mockk()
  private val blockRunClientMock: BlockRunClient = mockk()
  private val decryptionServiceMock: DecryptionService = mockk()

  @BeforeEach
  fun initSut() {
    every { gitLabClientFactoryMock.provideClientFor(any()) } returns gitlabClient
    every {
      blockRunClientMock.registerAsSource(
        any(),
        "Trigger GitLab CI/CD",
      )
    } returns Unit
    every { blockRunClientMock.updateBlockRun(any()) } returns Unit
    every { decryptionServiceMock.decrypt(any()) } answers { arg<String>(0) + "-decrypted" }

    sut = GitLabBlockRunnerService(
      blockRunClientFetcher = blockRunClientFetcherMock,
      gitlabClientFactory = gitLabClientFactoryMock,
      decryptionService = decryptionServiceMock,
    )
  }

  @Test
  fun `processBlock reports error when deserializing a GitLab response fails`() {
    val runWithLinks = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGitlabImplementation.test(),
    )

    every { blockRunClientFetcherMock.fetchBlockRunClient() } returns blockRunClientMock
    every { blockRunClientMock.activeBlockRun } returns runWithLinks

    every {
      gitlabClient.triggerPipeline(any(), any(), any(), any())
    } throws Exception()

    val result = sut.processBlock()

    assertThat(result).isNull()

    verify(exactly = 1) { blockRunClientMock.registerAsSource(any(), "Trigger GitLab CI/CD") }
    verify(atLeast = 1) { blockRunClientMock.updateBlockRun(any()) }
  }

  @Test
  fun `processBlock does nothing when there is no run available`() {
    every { blockRunClientFetcherMock.fetchBlockRunClient() } returns null

    val result = sut.processBlock()

    assertThat(result).isNull()
    verify(exactly = 0) { blockRunClientMock.registerAsSource(any(), any()) }
  }

  @Test
  fun `processBlock does nothing when there is an exception during run fetching`() {
    every { blockRunClientFetcherMock.fetchBlockRunClient() } throws Exception()

    val result = sut.processBlock()

    assertThat(result).isNull()
    verify(exactly = 0) { blockRunClientMock.registerAsSource(any(), any()) }
  }

  @Test
  fun `processBlock triggers works as expected`() {
    val runWithLinks = ProcessableBlockRun.test(
      implementation = MeshBuildingBlockGitlabImplementation.test(
        pipelineTriggerToken = "encryptedPipelineToken",
      ),
    )

    every { blockRunClientFetcherMock.fetchBlockRunClient() } returns blockRunClientMock
    every { blockRunClientMock.activeBlockRun } returns runWithLinks
    every { decryptionServiceMock.decryptBlockRunInputs(any()) } returnsArgument 0
    every { decryptionServiceMock.decrypt("encryptedPipelineToken") } returns "decryptedPipelineToken"
    every { gitlabClient.triggerPipeline(any(), any(), any(), any()) } returns Unit

    val result = sut.processBlock()

    verify(exactly = 1) {
      blockRunClientMock.registerAsSource(
        any(),
        "Trigger GitLab CI/CD",
      )
    }
    verify(atLeast = 1) { blockRunClientMock.updateBlockRun(any()) }

    verify(exactly = 1) {
      gitlabClient.triggerPipeline(
        pipelineToken = "decryptedPipelineToken",
        projectId = "123456",
        refName = "main",
        run = any(),
      )
    }

    verify { decryptionServiceMock.decryptBlockRunInputs(any()) }
    verify { decryptionServiceMock.decrypt("encryptedPipelineToken")  }

    assertThat(result).isNotNull
    assertThat(result!!.metadata.uuid).isEqualTo("test")
  }

  @Test
  fun `processBlock is called with decrypted inputs and removed implementation details`() {
    val runWithLinks = ProcessableBlockRun.test(
      inputs = listOf(
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
      ),
      implementation = MeshBuildingBlockGitlabImplementation.test(),
    )

    every { blockRunClientFetcherMock.fetchBlockRunClient() } returns blockRunClientMock
    every { blockRunClientMock.activeBlockRun } returns runWithLinks

    every { decryptionServiceMock.decryptBlockRunInputs(any()) } returnsArgument 0
    every { gitlabClient.triggerPipeline(any(), any(), any(), any()) } returns Unit

    val result = sut.processBlock()

    verify(exactly = 1) {
      gitlabClient.triggerPipeline(
        pipelineToken = "test123-decrypted",
        refName = "main",
        projectId = "123456",
        run = withArg { dispatchedRun ->
          val dispatchedInput = dispatchedRun.meshObject.spec.buildingBlock.spec.inputs
          assertThat(dispatchedInput).hasSize(4)
          assertThat(dispatchedInput.map { it.value }).containsExactlyInAnyOrder("1", "2", "3", 4)
        },
      )
    }

    assertThat(result).isNotNull
    assertThat(result!!.metadata.uuid).isEqualTo("test")

    verify { decryptionServiceMock.decryptBlockRunInputs(any()) }
  }
}
