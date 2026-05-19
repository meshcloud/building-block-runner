package io.meshcloud.buildingblocks.runner.github

import com.github.tomakehurst.wiremock.WireMockServer
import com.github.tomakehurst.wiremock.client.WireMock.configureFor
import com.github.tomakehurst.wiremock.core.WireMockConfiguration.wireMockConfig
import org.junit.jupiter.api.AfterAll
import org.junit.jupiter.api.BeforeAll

abstract class WiremockTestBase {

  companion object {
    @JvmStatic
    protected lateinit var wireMockServer: WireMockServer
    private val PORT: Int = (60000..65000).random()

    val BASE_URL = "http://localhost:$PORT"

    @BeforeAll
    @JvmStatic
    fun startServer() {
      wireMockServer = WireMockServer(wireMockConfig().port(PORT))
      wireMockServer.start()
      configureFor("localhost", PORT)
    }

    @AfterAll
    @JvmStatic
    fun stopServer() {
      wireMockServer.stop()
    }
  }
}
