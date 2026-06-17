package io.meshcloud.buildingblocks.runner.http.auth

import com.github.tomakehurst.wiremock.WireMockServer
import com.github.tomakehurst.wiremock.client.WireMock.*
import com.github.tomakehurst.wiremock.core.WireMockConfiguration.wireMockConfig
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.jupiter.api.AfterAll
import org.junit.jupiter.api.AfterEach
import org.junit.jupiter.api.Assertions.assertEquals
import org.junit.jupiter.api.Assertions.assertThrows
import org.junit.jupiter.api.Assertions.assertTrue
import org.junit.jupiter.api.BeforeAll
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test
import java.io.IOException

class ApiKeyAuthInterceptorTest {

  companion object {
    private lateinit var wireMockServer: WireMockServer

    @BeforeAll
    @JvmStatic
    fun startServer() {
      wireMockServer = WireMockServer(wireMockConfig().dynamicPort())
      wireMockServer.start()
    }

    @AfterAll
    @JvmStatic
    fun stopServer() {
      wireMockServer.stop()
    }
  }

  private lateinit var baseUrl: String
  private lateinit var interceptor: ApiKeyAuthInterceptor
  private lateinit var client: OkHttpClient

  @BeforeEach
  fun setup() {
    baseUrl = "http://localhost:${wireMockServer.port()}"
    interceptor = ApiKeyAuthInterceptor(
      baseUrl = baseUrl,
      clientId = "test-client-id",
      clientSecret = "test-client-secret",
      loginHttpClient = OkHttpClient(),
    )
    client = OkHttpClient.Builder()
      .addInterceptor(interceptor)
      .build()

    // Stub a generic endpoint that the interceptor-equipped client will call
    wireMockServer.stubFor(
      get(urlPathEqualTo("/api/runs"))
        .willReturn(aResponse().withStatus(200)),
    )
  }

  @AfterEach
  fun resetWireMock() {
    wireMockServer.resetAll()
  }

  private fun stubLoginSuccess(token: String, expiresIn: Int = 3600) {
    wireMockServer.stubFor(
      post(urlPathEqualTo("/api/login"))
        .willReturn(
          aResponse()
            .withStatus(200)
            .withHeader("Content-Type", "application/json")
            .withBody("""{"access_token":"$token","expires_in":$expiresIn}"""),
        ),
    )
  }

  private fun makeTestRequest() {
    client.newCall(
      Request.Builder().url("$baseUrl/api/runs").get().build(),
    ).execute().close()
  }

  // ------------------------------------------------------------------ fetch --

  @Test
  fun `fetches token on first request and sets Authorization header`() {
    stubLoginSuccess("my-access-token")

    makeTestRequest()

    wireMockServer.verify(
      getRequestedFor(urlPathEqualTo("/api/runs"))
        .withHeader("Authorization", equalTo("Bearer my-access-token")),
    )
  }

  @Test
  fun `login request sends correct JSON body with clientId and clientSecret`() {
    stubLoginSuccess("some-token")

    makeTestRequest()

    wireMockServer.verify(
      postRequestedFor(urlPathEqualTo("/api/login"))
        .withRequestBody(matchingJsonPath("$.clientId", equalTo("test-client-id")))
        .withRequestBody(matchingJsonPath("$.clientSecret", equalTo("test-client-secret")))
        .withHeader("Content-Type", containing("application/json")),
    )
  }

  // ----------------------------------------------------------------- cache --

  @Test
  fun `caches token and calls login only once for multiple requests`() {
    stubLoginSuccess("cached-token")

    makeTestRequest()
    makeTestRequest()
    makeTestRequest()

    // Login endpoint must be called exactly once; subsequent requests use the cached token.
    wireMockServer.verify(1, postRequestedFor(urlPathEqualTo("/api/login")))

    wireMockServer.verify(
      3,
      getRequestedFor(urlPathEqualTo("/api/runs"))
        .withHeader("Authorization", equalTo("Bearer cached-token")),
    )
  }

  // --------------------------------------------------------------- refresh --

  @Test
  fun `refreshes token when cached token has expired`() {
    // expires_in=31 → effective lifetime = 31 - 30 = 1s, so the token expires after 1s
    wireMockServer.stubFor(
      post(urlPathEqualTo("/api/login"))
        .inScenario("token-refresh")
        .whenScenarioStateIs("Started")
        .willReturn(
          aResponse()
            .withStatus(200)
            .withHeader("Content-Type", "application/json")
            .withBody("""{"access_token":"token-v1","expires_in":31}"""),
        )
        .willSetStateTo("refreshed"),
    )
    wireMockServer.stubFor(
      post(urlPathEqualTo("/api/login"))
        .inScenario("token-refresh")
        .whenScenarioStateIs("refreshed")
        .willReturn(
          aResponse()
            .withStatus(200)
            .withHeader("Content-Type", "application/json")
            .withBody("""{"access_token":"token-v2","expires_in":3600}"""),
        ),
    )

    makeTestRequest()
    wireMockServer.verify(
      getRequestedFor(urlPathEqualTo("/api/runs"))
        .withHeader("Authorization", equalTo("Bearer token-v1")),
    )
    wireMockServer.verify(1, postRequestedFor(urlPathEqualTo("/api/login")))

    // Wait for the 1s token lifetime to elapse
    Thread.sleep(1_100)

    makeTestRequest()
    wireMockServer.verify(2, postRequestedFor(urlPathEqualTo("/api/login")))
    wireMockServer.verify(
      getRequestedFor(urlPathEqualTo("/api/runs"))
        .withHeader("Authorization", equalTo("Bearer token-v2")),
    )
  }

  // --------------------------------------------------------------- failure --

  @Test
  fun `throws IOException with HTTP status code in message when login returns 403`() {
    wireMockServer.stubFor(
      post(urlPathEqualTo("/api/login"))
        .willReturn(aResponse().withStatus(403).withBody("Forbidden")),
    )

    val ex = assertThrows(IOException::class.java) { makeTestRequest() }

    assertTrue(
      ex.message?.contains("403") == true,
      "Exception message should contain the HTTP status code 403, but was: ${ex.message}",
    )
  }

  @Test
  fun `throws IOException when login returns 500`() {
    wireMockServer.stubFor(
      post(urlPathEqualTo("/api/login"))
        .willReturn(aResponse().withStatus(500).withBody("Internal Server Error")),
    )

    val ex = assertThrows(IOException::class.java) { makeTestRequest() }

    assertTrue(
      ex.message?.contains("500") == true,
      "Exception message should contain the HTTP status code 500, but was: ${ex.message}",
    )
  }

  @Test
  fun `throws IOException when login returns 401`() {
    wireMockServer.stubFor(
      post(urlPathEqualTo("/api/login"))
        .willReturn(aResponse().withStatus(401).withBody("Unauthorized - invalid client credentials")),
    )

    val ex = assertThrows(IOException::class.java) { makeTestRequest() }

    assertTrue(
      ex.message?.contains("401") == true,
      "Exception message should contain the HTTP status code 401, but was: ${ex.message}",
    )
  }

  // ------------------------------------------------------ edge: min expiry --

  @Test
  fun `clamps token lifetime to 1s minimum when expires_in is less than buffer`() {
    // expires_in=1 → 1 - 30 = -29s → clamped to 1s
    // Token should still be used for the first request but re-fetched very quickly.
    stubLoginSuccess("short-lived-token", expiresIn = 1)

    makeTestRequest()

    wireMockServer.verify(
      getRequestedFor(urlPathEqualTo("/api/runs"))
        .withHeader("Authorization", equalTo("Bearer short-lived-token")),
    )
    // After the first fetch the token is cached; verify login was called exactly once.
    wireMockServer.verify(1, postRequestedFor(urlPathEqualTo("/api/login")))
  }
}
