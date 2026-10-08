package io.meshcloud.buildingblocks.runner.runclient

import io.meshcloud.buildingblocks.runner.BlockRunnerApiConfig
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.objects.MeshBuildingBlockGitlabImplementation
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import okhttp3.OkHttpClient
import okhttp3.mockwebserver.Dispatcher
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import okhttp3.mockwebserver.RecordedRequest
import okhttp3.mockwebserver.SocketPolicy
import org.assertj.core.api.Assertions.assertThat
import org.assertj.core.api.Assertions.assertThatThrownBy
import org.junit.jupiter.api.AfterEach
import org.junit.jupiter.api.BeforeEach
import org.junit.jupiter.api.Test
import java.time.Duration

class HttpBlockRunClientTest {

  private val server = MockWebServer()
  private val clock = SteppingClock()
  private val noWaitRetry = MeshStackRetry(clock) { clock.advance(it) }

  @BeforeEach
  fun startServer() {
    server.start()
  }

  @AfterEach
  fun stopServer() {
    server.shutdown()
  }

  @Test
  fun `source registration is delivered once meshStack is back after 503`() {
    server.enqueue(MockResponse().setResponseCode(503))
    server.enqueue(MockResponse().setResponseCode(409))

    client().registerAsSource("step", "Step")

    assertThat(server.requestCount).isEqualTo(2)
  }

  @Test
  fun `source registration that meshStack never takes fails with the last rejection`() {
    server.dispatcher = alwaysAnswering(503)

    assertThatThrownBy { client().registerAsSource("step", "Step") }
      .isInstanceOf(MeshStackRejectedRequestException::class.java)
      .hasMessageContaining("503")
  }

  @Test
  fun `status update that meshStack never takes is reported as outcome unknown`() {
    server.dispatcher = alwaysAnswering(503)

    assertThatThrownBy { client().updateBlockRun(finalStatus()) }
      .isInstanceOf(MeshStackRequestOutcomeUnknownException::class.java)
  }

  @Test
  fun `an attempt that gets no answer is retried`() {
    server.enqueue(MockResponse().setSocketPolicy(SocketPolicy.NO_RESPONSE))
    server.enqueue(MockResponse().setResponseCode(200))
    val httpClient = OkHttpClient.Builder().callTimeout(Duration.ofMillis(200)).build()

    client(httpClient).updateBlockRun(finalStatus())

    assertThat(server.requestCount).isEqualTo(2)
  }

  @Test
  fun `a status code outside the HTTP standard is reported as rejected by meshStack`() {
    server.enqueue(MockResponse().setResponseCode(520).setBody("origin error"))

    assertThatThrownBy { client().updateBlockRun(finalStatus()) }
      .isInstanceOf(MeshStackRejectedRequestException::class.java)
      .hasMessage("meshStack answered HTTP 520: origin error")
    assertThat(server.requestCount).isEqualTo(1)
  }

  @Test
  fun `a rejection whose body breaks off keeps its status code`() {
    server.enqueue(MockResponse().setResponseCode(400).setBody("invalid output").setSocketPolicy(SocketPolicy.DISCONNECT_DURING_RESPONSE_BODY))

    assertThatThrownBy { client().updateBlockRun(finalStatus()) }
      .isInstanceOf(MeshStackRejectedRequestException::class.java)
      .hasMessageStartingWith("meshStack answered HTTP 400")
    assertThat(server.requestCount).isEqualTo(1)
  }

  private fun alwaysAnswering(statusCode: Int) = object : Dispatcher() {
    override fun dispatch(request: RecordedRequest) = MockResponse().setResponseCode(statusCode)
  }

  private fun client(httpClient: OkHttpClient = OkHttpClient()) = HttpBlockRunClient(
    activeBlockRun = ProcessableBlockRun.test(implementation = MeshBuildingBlockGitlabImplementation.test()),
    urlProvider = object : UrlProvider {
      override fun getRegisterSourceUrl() = server.url("/status/source").toString()

      override fun getUpdateSourceUrl() = server.url("/status/source/runner").toString()
    },
    httpClient = httpClient,
    config = BlockRunnerApiConfig(uuid = "runner", version = "test"),
    retry = noWaitRetry,
  )

  private fun finalStatus() = MeshBuildingBlockRun.SourceUpdate(
    status = MeshBuildingBlockRun.ExecutionStatus.SUCCEEDED,
    steps = emptyList(),
  )
}
