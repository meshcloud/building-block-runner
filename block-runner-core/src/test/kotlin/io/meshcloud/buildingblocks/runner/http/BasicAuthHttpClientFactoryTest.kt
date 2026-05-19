package io.meshcloud.buildingblocks.runner.http

import io.meshcloud.buildingblocks.runner.BlockRunnerApiConfig
import io.mockk.every
import io.mockk.mockk
import org.junit.jupiter.api.Assertions.assertNotNull
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Test

class BasicAuthHttpClientFactoryTest {

  @Test
  fun `buildClient returns configured OkHttpClient`() {
    // Given
    val authConfig = BlockRunnerApiConfig.BlockRunnerAuthConfig(
      username = "test-user",
      password = "test-password"
    )
    val config = mockk<BlockRunnerApiConfig> {
      every { auth } returns authConfig
    }
    val factory = BasicAuthHttpClientFactory(config)

    // When
    val client = factory.buildHttpClient()

    // Then
    assertNotNull(client, "Client should not be null")
    assertTrue(client.interceptors.isNotEmpty(), "Client should have interceptors configured")
  }

  @Test
  fun `buildClient returns same client instance on multiple calls`() {
    // Given
    val authConfig = BlockRunnerApiConfig.BlockRunnerAuthConfig(
      username = "test-user",
      password = "test-password"
    )
    val config = mockk<BlockRunnerApiConfig> {
      every { auth } returns authConfig
    }
    val factory = BasicAuthHttpClientFactory(config)

    // When
    val client1 = factory.buildHttpClient()
    val client2 = factory.buildHttpClient()

    // Then
    assertTrue(client1 === client2, "Factory should return the same client instance")
  }

  @Test
  fun `buildClient configures client to not follow redirects`() {
    // Given
    val authConfig = BlockRunnerApiConfig.BlockRunnerAuthConfig(
      username = "test-user",
      password = "test-password"
    )
    val config = mockk<BlockRunnerApiConfig> {
      every { auth } returns authConfig
    }
    val factory = BasicAuthHttpClientFactory(config)

    // When
    val client = factory.buildHttpClient()

    // Then
    assertTrue(!client.followRedirects, "Client should not follow redirects")
  }
}

