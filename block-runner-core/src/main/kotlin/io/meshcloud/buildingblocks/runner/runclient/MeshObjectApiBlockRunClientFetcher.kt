package io.meshcloud.buildingblocks.runner.runclient

import io.meshcloud.buildingblocks.runner.BlockRunnerApiConfig
import io.meshcloud.buildingblocks.runner.StandaloneBlockRunnerApiConfig
import io.meshcloud.buildingblocks.runner.http.AuthHttpClientFactory
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.http.EMPTY_REQUEST_BODY
import io.meshcloud.meshobjects.MeshHalMediaTypes
import io.github.oshai.kotlinlogging.KotlinLogging
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.Request
import org.springframework.context.annotation.Profile
import org.springframework.http.HttpStatus
import org.springframework.stereotype.Component

private val log = KotlinLogging.logger { }

/**
 * Only present if no kubernetes profile is active, and we are expected to
 * fetch runs directly from the meshObject API.
 */
@Component
@Profile("!kubernetes")
class MeshObjectApiBlockRunClientFetcher(
  authHttpClientFactory: AuthHttpClientFactory,
  private val blockRunClientFactory: BlockRunClientFactory,
  private val standaloneBlockRunnerApiConfig: StandaloneBlockRunnerApiConfig,
  private val config: BlockRunnerApiConfig,
  private val processableRunFactory: ProcessableRunFactory,
) : BlockRunClientFetcher {

  private val httpClient = authHttpClientFactory.buildHttpClient()

  override fun fetchBlockRunClient(): BlockRunClient? {
    val url = standaloneBlockRunnerApiConfig.api.url.toHttpUrl().newBuilder()
      .addPathSegments("api/meshobjects/meshbuildingblockruns/create")
      .addQueryParameter("forRunnerUuid", config.uuid)
      .build()

    val request = Request.Builder()
      .url(url)
      .post(EMPTY_REQUEST_BODY)
      .addHeader("Content-Type", MeshHalMediaTypes.MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1)
      .addHeader("Accept", MeshHalMediaTypes.MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1)
      .build()

    log.debug { "Requesting blocks from API: ${request.url}" }

    val run = executeFetchRun(request)
      ?: return null

    return blockRunClientFactory.buildBlockRunClient(run)
  }

  private fun executeFetchRun(request: Request): ProcessableBlockRun? {
    return httpClient.newCall(request).execute().use { response ->
      when (HttpStatus.valueOf(response.code)) {
        HttpStatus.NOT_FOUND -> {
          log.debug { "No new blocks returned" }
          null
        }

        HttpStatus.CONFLICT -> {
          log.warn { "There was probably a race condition on the coordinator, retrying..." }
          null
        }

        HttpStatus.OK, HttpStatus.CREATED -> {
          val bodyStr = response.body?.string()
            ?: throw IllegalStateException("Body was null")

          log.debug { "Response Body:\n$bodyStr" }

          return processableRunFactory.buildProcessableRun(bodyStr)
        }

        else -> {
          throw IllegalStateException("Unexpected HTTP code: ${response.code}, body: ${response.body?.string()}")
        }
      }
    }
  }
}
