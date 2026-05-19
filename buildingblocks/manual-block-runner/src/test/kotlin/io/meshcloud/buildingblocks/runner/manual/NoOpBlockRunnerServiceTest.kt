package io.meshcloud.buildingblocks.runner.manual

import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClient
import io.meshcloud.buildingblocks.runner.runclient.BlockRunClientFetcher
import io.meshcloud.meshobjects.objects.MeshBuildingBlockIOType
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import io.meshcloud.meshobjects.objects.MeshManualBuildingBlockImplementation
import io.mockk.MockKAnnotations
import io.mockk.every
import io.mockk.impl.annotations.MockK
import io.mockk.verify
import org.assertj.core.api.Assertions.assertThat
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test

class NoOpBlockRunnerServiceTest {

  @MockK
  private lateinit var blockRunClientFetcher: BlockRunClientFetcher

  @MockK
  private lateinit var blockRunClient: BlockRunClient

  private lateinit var service: NoOpBlockRunnerService

  @BeforeEach
  fun setUp() {
    MockKAnnotations.init(this, relaxUnitFun = true)

    service = NoOpBlockRunnerService(blockRunClientFetcher = blockRunClientFetcher)
  }

  @Test
  fun `processBlock returns null when there is no run available`() {
    every { blockRunClientFetcher.fetchBlockRunClient() } returns null

    val result = service.processBlock()

    assertThat(result).isNull()
    verify(exactly = 0) { blockRunClient.registerAsSource(any(), any()) }
  }

  @Test
  fun `processBlock returns null when there is an exception during run fetching`() {
    every { blockRunClientFetcher.fetchBlockRunClient() } throws Exception()

    val result = service.processBlock()

    assertThat(result).isNull()
    verify(exactly = 0) { blockRunClient.registerAsSource(any(), any()) }
  }

  @Test
  fun `processBlock registers and updates block run when run is available`() {
    val processableBlockRun = ProcessableBlockRun.test(
      implementation = MeshManualBuildingBlockImplementation()
    )
    every { blockRunClientFetcher.fetchBlockRunClient() } returns blockRunClient
    every { blockRunClient.activeBlockRun } returns processableBlockRun
    every { blockRunClient.registerAsSource(any(), any()) } returns Unit
    every { blockRunClient.updateBlockRun(any()) } returns Unit

    val result = service.processBlock()

    verify(exactly = 1) {
      blockRunClient.registerAsSource(
        stepId = "manual",
        stepDisplayName = "Manual Block Run"
      )
    }
    verify(exactly = 1) {
      blockRunClient.updateBlockRun(
        match { update ->
          update.status == MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED &&
            update.steps?.size == 1 &&
            update.steps!![0].id == "manual"
        }
      )
    }
    assertThat(result).isNotNull
    assertThat(result!!.metadata.uuid).isEqualTo("test")
  }

  @Test
  fun `toOutputType maps STRING to STRING`() {
    assertThat(NoOpBlockRunnerService.toOutputType(MeshBuildingBlockIOType.STRING)).isEqualTo(MeshBuildingBlockIOType.STRING)
  }

  @Test
  fun `toOutputType maps INTEGER to INTEGER`() {
    assertThat(NoOpBlockRunnerService.toOutputType(MeshBuildingBlockIOType.INTEGER)).isEqualTo(MeshBuildingBlockIOType.INTEGER)
  }

  @Test
  fun `toOutputType maps BOOLEAN to BOOLEAN`() {
    assertThat(NoOpBlockRunnerService.toOutputType(MeshBuildingBlockIOType.BOOLEAN)).isEqualTo(MeshBuildingBlockIOType.BOOLEAN)
  }

  @Test
  fun `toOutputType maps CODE to CODE`() {
    assertThat(NoOpBlockRunnerService.toOutputType(MeshBuildingBlockIOType.CODE)).isEqualTo(MeshBuildingBlockIOType.CODE)
  }

  @Test
  fun `toOutputType maps FILE to STRING`() {
    assertThat(NoOpBlockRunnerService.toOutputType(MeshBuildingBlockIOType.FILE)).isEqualTo(MeshBuildingBlockIOType.STRING)
  }

  @Test
  fun `toOutputType maps LIST to CODE`() {
    assertThat(NoOpBlockRunnerService.toOutputType(MeshBuildingBlockIOType.LIST)).isEqualTo(MeshBuildingBlockIOType.CODE)
  }

  @Test
  fun `toOutputType maps SINGLE_SELECT to STRING`() {
    assertThat(NoOpBlockRunnerService.toOutputType(MeshBuildingBlockIOType.SINGLE_SELECT)).isEqualTo(MeshBuildingBlockIOType.STRING)
  }

  @Test
  fun `toOutputType maps MULTI_SELECT to CODE`() {
    assertThat(NoOpBlockRunnerService.toOutputType(MeshBuildingBlockIOType.MULTI_SELECT)).isEqualTo(MeshBuildingBlockIOType.CODE)
  }
}
