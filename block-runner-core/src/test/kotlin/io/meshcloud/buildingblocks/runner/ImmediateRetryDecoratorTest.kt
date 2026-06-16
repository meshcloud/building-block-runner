package io.meshcloud.buildingblocks.runner

import io.mockk.every
import io.mockk.impl.annotations.MockK
import io.mockk.junit5.MockKExtension
import io.mockk.mockk
import io.mockk.verify
import org.junit.jupiter.api.Assertions.assertNull
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test
import org.junit.jupiter.api.extension.ExtendWith

@ExtendWith(MockKExtension::class)
class ImmediateRetryDecoratorTest {

  @MockK(relaxUnitFun = true)
  private lateinit var wrappedService: BlockRunnerService

  private lateinit var sut: ImmediateRetryDecorator

  @BeforeEach
  fun setup() {
    sut = ImmediateRetryDecorator(wrappedService)
  }

  @Test
  fun `processBlock returns null when wrapped service returns null immediately`() {
    // Given
    every { wrappedService.processBlock() } returns null

    // When
    val result = sut.processBlock()

    // Then
    assertNull(result)
    verify(exactly = 1) { wrappedService.processBlock() }
  }

  @Test
  fun `processBlock retries once when wrapped service returns block once then null`() {
    // Given
    val blockRun = mockk<io.meshcloud.meshobjects.objects.MeshBuildingBlockRun>()
    every { wrappedService.processBlock() } returnsMany listOf(blockRun, null)

    // When
    val result = sut.processBlock()

    // Then
    assertNull(result)
    verify(exactly = 2) { wrappedService.processBlock() }
  }

  @Test
  fun `processBlock retries multiple times when wrapped service returns blocks multiple times`() {
    // Given
    val blockRun1 = mockk<io.meshcloud.meshobjects.objects.MeshBuildingBlockRun>()
    val blockRun2 = mockk<io.meshcloud.meshobjects.objects.MeshBuildingBlockRun>()
    val blockRun3 = mockk<io.meshcloud.meshobjects.objects.MeshBuildingBlockRun>()
    every { wrappedService.processBlock() } returnsMany listOf(blockRun1, blockRun2, blockRun3, null)

    // When
    val result = sut.processBlock()

    // Then
    assertNull(result)
    verify(exactly = 4) { wrappedService.processBlock() }
  }

  @Test
  fun `processBlock always returns null regardless of wrapped service results`() {
    // Given
    val blockRun = mockk<io.meshcloud.meshobjects.objects.MeshBuildingBlockRun>()
    every { wrappedService.processBlock() } returnsMany listOf(blockRun, blockRun, null)

    // When
    val result = sut.processBlock()

    // Then
    assertNull(result)
  }
}

