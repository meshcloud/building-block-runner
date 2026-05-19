package io.meshcloud.buildingblocks.runner.runclient

import io.meshcloud.buildingblocks.runner.BlockRunnerApiConfig
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.http.addLogging
import io.meshcloud.http.auth.BearerAuthInterceptor
import mu.KotlinLogging
import okhttp3.OkHttpClient
import org.springframework.stereotype.Component

private val log = KotlinLogging.logger { }

@Component
class HttpRunTokenRunClientFactory(
  private val config: BlockRunnerApiConfig
) : BlockRunClientFactory {
  override fun buildBlockRunClient(run: ProcessableBlockRun): BlockRunClient {
    val httpClient = buildHttpClientWithRunnerToken(run)

    return HttpBlockRunClient(
      httpClient = httpClient,
      config = config,
      activeBlockRun = run
    )
  }

  private fun buildHttpClientWithRunnerToken(run: ProcessableBlockRun): OkHttpClient {
    val runnerToken = run.meshObject.spec.runToken

    return OkHttpClient.Builder()
      .followRedirects(false)
      .addLogging(log)
      .addInterceptor(
        BearerAuthInterceptor(
          token = runnerToken,
        )
      ).build()
  }
}
