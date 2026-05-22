package io.meshcloud.buildingblocks.runner.runclient

import io.meshcloud.buildingblocks.runner.BlockRunnerApiConfig
import io.meshcloud.buildingblocks.runner.meshobject.MeshObjectApiObjectMapper
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.MeshHalMediaTypes
import io.meshcloud.meshobjects.objects.MeshBuildingBlockRun
import io.github.oshai.kotlinlogging.KotlinLogging
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.springframework.http.HttpStatus

private val log = KotlinLogging.logger { }

class HttpBlockRunClient(
  override val activeBlockRun: ProcessableBlockRun,
  private val httpClient: OkHttpClient,
  private val config: BlockRunnerApiConfig
) : BlockRunClient {

  private val mapper = MeshObjectApiObjectMapper.mapper

  override fun registerAsSource(
    stepId: String,
    stepDisplayName: String,
  ) {
    val sourceRegistration = MeshBuildingBlockRun.BlockRunSourceRegistration(
      source = MeshBuildingBlockRun.BlockRunSourceRegistration.SourceRegistration(
        id = config.uuid
      ),
      steps = listOf(
        MeshBuildingBlockRun.BlockRunSourceRegistration.StepRegistration(
          id = stepId,
          displayName = stepDisplayName
        )
      )
    )
    val body = mapper.writeValueAsString(sourceRegistration)
      .toRequestBody(MeshHalMediaTypes.MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1.toMediaType())

    val runUuid = activeBlockRun.meshObject.metadata.uuid
    val url = config.api.url.toHttpUrl().newBuilder()
      .addPathSegments("api/meshobjects/meshbuildingblockruns/$runUuid/status/source")
      .build()

    val request = Request.Builder()
      .url(url)
      .post(body)
      .addHeader("Accept", MeshHalMediaTypes.MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1)
      .build()

    httpClient.newCall(request).execute().use { response ->
      when (HttpStatus.valueOf(response.code)) {
        HttpStatus.CONFLICT -> log.debug { "Sources ${config.uuid} was already registered" }
        HttpStatus.OK -> log.debug { "Registered as source." }
        else -> throw IllegalStateException("Unexpected HTTP code: ${response.code}, body: ${response.body?.string()}")
      }
    }
  }

  /**
   * We ignore the response of this call.
   * We could parse for the abortRun flag which is part of the response,
   * but as we never respect it anyway, we just omit it for now.
   */
  override fun updateBlockRun(
    sourceUpdate: MeshBuildingBlockRun.SourceUpdate
  ) {
    val body = mapper
      .writeValueAsString(sourceUpdate)
      .toRequestBody(MeshHalMediaTypes.MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1.toMediaType())

    val runUuid = activeBlockRun.meshObject.metadata.uuid
    val url = config.api.url.toHttpUrl().newBuilder()
      .addPathSegments("api/meshobjects/meshbuildingblockruns/$runUuid/status/source/${config.uuid}")
      .build()

    val request = Request.Builder()
      .url(url)
      .patch(body)
      .addHeader("Accept", MeshHalMediaTypes.MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1)
      .build()

    httpClient.newCall(request).execute().use { response ->
      when (HttpStatus.valueOf(response.code)) {
        HttpStatus.OK -> log.debug { "Block run updated successfully." }
        else -> throw IllegalStateException("Unexpected HTTP code: ${response.code}, body: ${response.body?.string()}")
      }
    }
  }
}
