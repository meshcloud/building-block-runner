package io.meshcloud.buildingblocks.runner.azuredevops.client

import com.fasterxml.jackson.annotation.JsonInclude
import com.fasterxml.jackson.databind.DeserializationFeature
import com.fasterxml.jackson.datatype.jdk8.Jdk8Module
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.registerKotlinModule
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.http.*
import io.meshcloud.http.MediaTypes.MEDIA_TYPE_JSON
import mu.KotlinLogging
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.util.*

private val log = KotlinLogging.logger { }

class AzureDevOpsClient(
  private val azureDevOpsBaseUrl: String,
  private val accessToken: String,
  private val organization: String,
  private val project: String,
  private val pipelineId: String,
  private val run: ProcessableBlockRun,
  private val refName: String? = null,
) {

  private data class RepositoryRef(val refName: String)

  private data class RepositoriesResources(val self: RepositoryRef)

  private data class PipelineResources(val repositories: RepositoriesResources)

  @JsonInclude(JsonInclude.Include.NON_NULL)
  private data class TriggerPipelinePayload(
    val templateParameters: Map<String, String> = emptyMap(),
    val resources: PipelineResources? = null,
  )

  private val client = OkHttpClient.Builder()
    .followRedirects(false)
    .addLogging(log)
    .build()

  private val mapper = jacksonObjectMapper()
    .registerModule(Jdk8Module())
    .registerModule(JavaTimeModule())
    .registerKotlinModule()
    .configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false)

  /**
   * Triggers a pipeline run in Azure DevOps
   */
  fun triggerPipeline(): PipelineRun {
    val url = "$azureDevOpsBaseUrl/$organization/$project/_apis/pipelines/$pipelineId/runs?api-version=7.1".toHttpUrl()

    val inputsAsParameters = run.meshObject.spec.buildingBlock.spec.inputs
      .filter { input -> !input.isEnvironment }
      .associate { input -> input.key to input.value.toString() }.toMutableMap()
    // We also need to provide context of the run behavior
    inputsAsParameters["MESHSTACK_BEHAVIOR"] = run.meshObject.spec.behavior.name

    val payload = TriggerPipelinePayload(
      templateParameters = inputsAsParameters,
      resources = refName?.let { PipelineResources(RepositoriesResources(RepositoryRef(it))) }
    )
    val payloadBody = mapper.writeValueAsString(payload)

    val request = buildTriggerRequest(url, payloadBody)

    return client.execute<PipelineRun>(request)
      .letWithStatus(HttpStatus.OK) {
        val pipelineRun = mapper.readValue(it.body, PipelineRun::class.java)
        it.handled(pipelineRun)
      }.getContent()
  }

  /**
   * Gets the status of a pipeline run
   */
  fun getPipelineRun(runId: Long): PipelineRun {
    val url = "$azureDevOpsBaseUrl/$organization/$project/_apis/pipelines/$pipelineId/runs/$runId?api-version=7.1".toHttpUrl()

    val request = Request.Builder()
      .url(url)
      .get()
      .addHeader("Accept", "application/json")
      .addAuthHeader()
      .build()

    return client.execute<PipelineRun>(request)
      .letWithStatus(HttpStatus.OK) {
        val pipelineRun = mapper.readValue(it.body, PipelineRun::class.java)
        it.handled(pipelineRun)
      }.getContent()
  }

  /**
   * Gets the timeline records for a pipeline run (includes stages, jobs, tasks)
   */
  fun getPipelineTimeline(runId: Long): List<TimelineRecord> {
    val url = "$azureDevOpsBaseUrl/$organization/$project/_apis/build/builds/$runId/timeline?api-version=7.1".toHttpUrl()

    val request = Request.Builder()
      .url(url)
      .get()
      .addHeader("Accept", "application/json")
      .addAuthHeader()
      .build()

    return client.execute<TimelineResponse>(request)
      .letWithStatus(HttpStatus.OK) {
        val response = mapper.readValue(it.body, TimelineResponse::class.java)
        it.handled(response)
      }.getContent().records
  }

  private fun Request.Builder.addAuthHeader(): Request.Builder {
    val encodedToken = Base64.getEncoder().encodeToString(":$accessToken".toByteArray())
    return addHeader("Authorization", "Basic $encodedToken")
  }

  private fun buildTriggerRequest(url: HttpUrl, payload: String): Request {
    return Request.Builder()
      .url(url)
      .post(payload.toRequestBody(MEDIA_TYPE_JSON))
      .addHeader("Accept", "application/json")
      .addAuthHeader()
      .build()
  }
}
