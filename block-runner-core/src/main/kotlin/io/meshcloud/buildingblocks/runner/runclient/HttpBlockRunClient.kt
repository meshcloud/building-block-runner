package io.meshcloud.buildingblocks.runner.runclient

import io.github.oshai.kotlinlogging.KotlinLogging
import io.meshcloud.buildingblocks.runner.BlockRunnerApiConfig
import io.meshcloud.buildingblocks.runner.meshobject.MeshObjectApiObjectMapper
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.MeshHalMediaTypes
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import org.springframework.http.HttpStatus

private val log = KotlinLogging.logger { }

class HttpBlockRunClient(
  override val activeBlockRun: ProcessableBlockRun,
  private val urlProvider: UrlProvider,
  private val httpClient: OkHttpClient,
  private val config: BlockRunnerApiConfig,
  private val retry: MeshStackRetry = MeshStackRetry(),
) : BlockRunClient {

  private val mapper = MeshObjectApiObjectMapper.mapper
  private val runId = activeBlockRun.meshObject.metadata.uuid

  override fun registerAsSource(
    stepId: String,
    stepDisplayName: String,
  ) {
    val sourceRegistration = MeshBuildingBlockRun.BlockRunSourceRegistration(
      source = MeshBuildingBlockRun.BlockRunSourceRegistration.SourceRegistration(
        id = config.uuid,
      ),
      steps = listOf(
        MeshBuildingBlockRun.BlockRunSourceRegistration.StepRegistration(
          id = stepId,
          displayName = stepDisplayName,
        ),
      ),
    )
    val body = mapper.writeValueAsString(sourceRegistration)
      .toRequestBody(MeshHalMediaTypes.MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1.toMediaType())

    val url = urlProvider.getRegisterSourceUrl().toHttpUrl()

    val request = Request.Builder()
      .url(url)
      .post(body)
      .addHeader("Accept", MeshHalMediaTypes.MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1)
      .build()

    // A second pod can register again: meshStack answers 409 for a registration that already arrived.
    retry.call(runId, "source registration", outcomeUnknownAfterRetry = false) {
      httpClient.newCall(request).execute().use { response ->
        when (response.code) {
          HttpStatus.CONFLICT.value() -> log.debug { "Sources ${config.uuid} was already registered" }
          HttpStatus.OK.value() -> log.debug { "Registered as source." }
          else -> throw rejected(response)
        }
      }
    }
  }

  /**
   * We ignore the response of this call.
   * We could parse for the abortRun flag which is part of the response,
   * but as we never respect it anyway, we just omit it for now.
   */
  override fun updateBlockRun(
    sourceUpdate: MeshBuildingBlockRun.SourceUpdate,
  ) {
    val body = mapper
      .writeValueAsString(sourceUpdate)
      .toRequestBody(MeshHalMediaTypes.MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1.toMediaType())

    val url = urlProvider.getUpdateSourceUrl().toHttpUrl()

    val request = Request.Builder()
      .url(url)
      .patch(body)
      .addHeader("Accept", MeshHalMediaTypes.MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1)
      .build()

    retry.call(runId, "status update", outcomeUnknownAfterRetry = true) {
      httpClient.newCall(request).execute().use { response ->
        when (response.code) {
          HttpStatus.OK.value() -> log.debug { "Block run updated successfully." }
          else -> throw rejected(response)
        }
      }
    }
  }

  private fun rejected(response: Response) = MeshStackRejectedRequestException(
    statusCode = response.code,
    message = "meshStack answered HTTP ${response.code}: ${runCatching { response.body?.string() }.getOrElse { "<unreadable body: ${it.message}>" }}",
  )
}
