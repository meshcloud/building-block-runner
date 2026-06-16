package io.meshcloud.buildingblocks.runner.security

import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.objects.MeshManualBuildingBlockImplementation
import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertSame
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test

class NoOpDecryptionServiceTest {

  private lateinit var sut: NoOpDecryptionService

  @BeforeEach
  fun setup() {
    sut = NoOpDecryptionService()
  }

  @Test
  fun `decrypt returns the same string without modification`() {
    // Given
    val secret = "encrypted-secret-value"

    // When
    val result = sut.decrypt(secret)

    // Then
    assertEquals(secret, result)
  }

  @Test
  fun `decrypt handles empty string`() {
    // Given
    val secret = ""

    // When
    val result = sut.decrypt(secret)

    // Then
    assertEquals(secret, result)
  }

  @Test
  fun `decryptBlockRunInputs returns the same run without modification`() {
    // Given
    val run = ProcessableBlockRun.test(implementation = MeshManualBuildingBlockImplementation())

    // When
    val result = sut.decryptBlockRunInputs(run)

    // Then
    assertSame(run, result)
  }
}

