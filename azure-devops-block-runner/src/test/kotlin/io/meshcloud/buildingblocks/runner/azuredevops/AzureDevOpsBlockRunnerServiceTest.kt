package io.meshcloud.buildingblocks.runner.azuredevops

import io.meshcloud.buildingblocks.runner.azuredevops.client.AzureDevOpsClient
import io.meshcloud.buildingblocks.runner.azuredevops.client.AzureDevOpsClientFactory
import io.meshcloud.buildingblocks.runner.azuredevops.client.PipelineRun
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClient
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.meshobjects.objects.MeshBuildingBlockAzureDevOpsImplementation
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import io.mockk.*
import io.mockk.impl.annotations.MockK
import io.mockk.junit5.MockKExtension
import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertNull
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.extension.ExtendWith

@ExtendWith(MockKExtension::class)
class AzureDevOpsBlockRunnerServiceTest {
  @MockK(relaxed = true)
  private lateinit var blockRunClientFetcher: BlockRunClientFetcher

  @MockK(relaxed = true)
  private lateinit var blockRunClient: BlockRunClient

  @MockK(relaxed = true)
  private lateinit var azureDevOpsClientFactory: AzureDevOpsClientFactory

  private lateinit var sut: AzureDevOpsBlockRunnerService

  @BeforeEach
  fun setUp() {
    sut = AzureDevOpsBlockRunnerService(blockRunClientFetcher, azureDevOpsClientFactory)

    every { blockRunClientFetcher.fetchBlockRunClient() } returns blockRunClient
  }

  @Test
  fun `processBlock returns null when fetchBlockRun throws`() {
    every { blockRunClientFetcher.fetchBlockRunClient() } throws RuntimeException("fetch error")
    val result = sut.processBlock()
    assertNull(result)
  }

  @Test
  fun `processBlock returns null when fetchBlockRun returns null`() {
    every { blockRunClientFetcher.fetchBlockRunClient() } returns null
    val result = sut.processBlock()
    assertNull(result)
  }

  @Test
  fun `processBlock triggers pipeline and polls for sync implementation`() {
    val blockRun = mockk<MeshBuildingBlockRun>(relaxed = true)
    val blockRunWithLinks = mockk<ProcessableBlockRun>(relaxed = true) {
      every { meshObject } returns blockRun
    }
    val implementation = mockk<MeshBuildingBlockAzureDevOpsImplementation> {
      every { async } returns false
    }
    val azureDevOpsClient = mockk<AzureDevOpsClient>(relaxed = true)
    val pipelineRun = mockk<PipelineRun>(relaxed = true)
    mockkObject(AzureDevOpsPipelinePoller)

    every {
      AzureDevOpsPipelinePoller.pollPipelineCompletion(
        azureDevOpsClient = azureDevOpsClient,
        blockRun = blockRun,
        pipelineRun = pipelineRun,
        statusUpdater = any(),
      )
    } returns Unit

    every { blockRunClient.activeBlockRun } returns blockRunWithLinks
    every { blockRun.getImplementation<MeshBuildingBlockAzureDevOpsImplementation>() } returns implementation
    every { azureDevOpsClientFactory.provideClientFor(blockRunWithLinks) } returns azureDevOpsClient
    every { azureDevOpsClient.triggerPipeline() } returns pipelineRun

    val result = sut.processBlock()
    assertEquals(blockRun, result)
    verify { azureDevOpsClient.triggerPipeline() }
    verify {
      AzureDevOpsPipelinePoller.pollPipelineCompletion(
        azureDevOpsClient = azureDevOpsClient,
        blockRun = blockRun,
        pipelineRun = pipelineRun,
        statusUpdater = any(),
      )
    }
  }
}
