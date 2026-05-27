package io.meshcloud.buildingblocks.runner.http

import io.meshcloud.buildingblocks.runner.BlockRunnerApiConfig
import io.meshcloud.buildingblocks.runner.StandaloneBlockRunnerApiConfig
import io.meshcloud.buildingblocks.runner.http.auth.ApiKeyAuthInterceptor
import io.meshcloud.buildingblocks.runner.http.auth.BasicAuthInterceptor
import io.mockk.every
import io.mockk.mockk
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertNotNull
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.Test

class AuthHttpClientFactoryTest {

  private fun buildBasicAuthConfig(username: String = "test-user", password: String = "test-password") =
    mockk<StandaloneBlockRunnerApiConfig> {
      every { api } returns StandaloneBlockRunnerApiConfig.ApiConfig(url = "http://localhost:8080")
      every { auth } returns StandaloneBlockRunnerApiConfig.AuthConfig(
        username = username,
        password = password,
        apiKey = null,
      )
    }

  private fun buildApiKeyConfig(clientId: String = "test-client-id", clientSecret: String = "test-secret") =
    mockk<StandaloneBlockRunnerApiConfig> {
      every { api } returns StandaloneBlockRunnerApiConfig.ApiConfig(url = "http://localhost:8080")
      every { auth } returns StandaloneBlockRunnerApiConfig.AuthConfig(
        username = null,
        password = null,
        apiKey = StandaloneBlockRunnerApiConfig.ApiKeyConfig(
          clientId = clientId,
          clientSecret = clientSecret,
        ),
      )
    }

  private fun buildRunnerConfig(version: String = "dev") =
    mockk<BlockRunnerApiConfig> {
      every { this@mockk.version } returns version
    }

  // -------------------------------------------------------- basic auth path --

  @Test
  fun `buildClient returns configured OkHttpClient for basic auth`() {
    val client = AuthHttpClientFactory(buildBasicAuthConfig(), buildRunnerConfig()).buildHttpClient()

    assertNotNull(client, "Client should not be null")
    assertTrue(client.interceptors.isNotEmpty(), "Client should have interceptors configured")
  }

  @Test
  fun `buildClient returns same client instance on multiple calls`() {
    val factory = AuthHttpClientFactory(buildBasicAuthConfig(), buildRunnerConfig())

    assertTrue(factory.buildHttpClient() === factory.buildHttpClient(), "Factory should return the same client instance")
  }

  @Test
  fun `buildClient configures client to not follow redirects`() {
    val client = AuthHttpClientFactory(buildBasicAuthConfig(), buildRunnerConfig()).buildHttpClient()

    assertTrue(!client.followRedirects, "Client should not follow redirects")
  }

  @Test
  fun `buildClient uses BasicAuthInterceptor when only username and password are configured`() {
    val client = AuthHttpClientFactory(buildBasicAuthConfig(), buildRunnerConfig()).buildHttpClient()

    val hasBasicAuth = client.interceptors.any { it is BasicAuthInterceptor }
    assertTrue(hasBasicAuth, "Client should have a BasicAuthInterceptor when apiKey is not configured")
  }

  // --------------------------------------------------------- api key path --

  @Test
  fun `buildClient uses ApiKeyAuthInterceptor when apiKey is configured`() {
    val client = AuthHttpClientFactory(buildApiKeyConfig(), buildRunnerConfig()).buildHttpClient()

    val hasApiKeyAuth = client.interceptors.any { it is ApiKeyAuthInterceptor }
    assertTrue(hasApiKeyAuth, "Client should have an ApiKeyAuthInterceptor when apiKey is configured")
  }

  @Test
  fun `buildClient does not use BasicAuthInterceptor when apiKey is configured`() {
    val client = AuthHttpClientFactory(buildApiKeyConfig(), buildRunnerConfig()).buildHttpClient()

    val hasBasicAuth = client.interceptors.any { it is BasicAuthInterceptor }
    assertTrue(!hasBasicAuth, "Client should not have a BasicAuthInterceptor when apiKey is configured")
  }

  @Test
  fun `buildClient configures client to not follow redirects when using API key`() {
    val client = AuthHttpClientFactory(buildApiKeyConfig(), buildRunnerConfig()).buildHttpClient()

    assertTrue(!client.followRedirects, "Client should not follow redirects")
  }

  @Test
  fun `buildClient adds runner version header to outgoing requests`() {
    val server = MockWebServer()
    server.start()

    try {
      server.enqueue(MockResponse().setResponseCode(200).setBody("ok"))

      val factory = AuthHttpClientFactory(buildBasicAuthConfig(), buildRunnerConfig(version = "1.2.3"))

      val request = Request.Builder()
        .url(server.url("/mesh-api-test"))
        .get()
        .build()

      factory.buildHttpClient().newCall(request).execute().close()

      val recordedRequest = server.takeRequest()
      assertEquals("1.2.3", recordedRequest.getHeader("X-Meshcloud-Runner-Version"))
    } finally {
      server.shutdown()
    }
  }
}

