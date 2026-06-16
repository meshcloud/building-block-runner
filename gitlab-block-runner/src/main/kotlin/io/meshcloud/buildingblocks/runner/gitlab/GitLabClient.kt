package io.meshcloud.buildingblocks.runner.gitlab

import com.fasterxml.jackson.databind.DeserializationFeature
import com.fasterxml.jackson.datatype.jdk8.Jdk8Module
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.registerKotlinModule
import io.meshcloud.buildingblocks.runner.http.addLogging
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.buildingblocks.runner.http.MeshHttpException
import io.github.oshai.kotlinlogging.KotlinLogging
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MultipartBody
import okhttp3.OkHttpClient
import okhttp3.Request

private val log = KotlinLogging.logger { }

class GitLabClient(
  private val gitlabBaseUrl: String
) {

  private class GitLabErrorBody(
    val message: GitLabBase
  ) {
    data class GitLabBase(
      val base: List<String>
    )

    fun isIdentityVerificationRequired(): Boolean {
      return message.base.any { it == "Identity verification is required in order to run CI jobs" }
    }
  }

  private val client = OkHttpClient.Builder()
    .followRedirects(false)
    .addLogging(log)
    .build()

  private val mapper = jacksonObjectMapper()
    .registerModule(Jdk8Module())
    .registerModule(JavaTimeModule())
    .registerKotlinModule()
    .configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false)

  fun triggerPipeline(
    pipelineToken: String,
    refName: String,
    projectId: String,
    run: ProcessableBlockRun,
  ) {
    val url = "$gitlabBaseUrl/api/v4/projects/$projectId/trigger/pipeline".toHttpUrl()

    val body = buildPayload(
      pipelineToken = pipelineToken,
      refName = refName,
      run = run
    )

    val request = Request.Builder()
      .url(url)
      .post(body)
      .build()

    client.newCall(request).execute().use { response ->
      val body = response.body?.string() ?: ""
      if (response.isSuccessful) return

      if (response.code == 404) {
        throw MeshHttpException(
          userMessage = "GitLab pipeline could not be triggered successfully. Please contact support.",
          systemMessage = "GitLab reported 404, which can happen if you have entered a wrong projectId.",
          statusCode = response.code,
          requestUrl = request.url,
          responseBody = body,
        )
      }

      val gitlabError = try {
        mapper.readValue(body, GitLabErrorBody::class.java)
      } catch (e: Exception) {
        log.error(e) { "Could not deserialize the GitLab error response." }
        throw MeshHttpException(
          userMessage = "There was a problem while communicating with GitLab.",
          statusCode = response.code,
          requestUrl = request.url,
          responseBody = body,
        )
      }

      if (gitlabError.isIdentityVerificationRequired()) {
        throw MeshHttpException(
          userMessage = "There is a problem with the pipeline trigger token. Please contact support.",
          systemMessage = "Your GitLab account is not verified and can not trigger a pipeline. " +
            "Please visit GitLab and verify your account first.",
          statusCode = response.code,
          requestUrl = request.url,
          responseBody = body,
        )
      }
      throw MeshHttpException(
        userMessage = "There was an error communicating with GitLab.",
        systemMessage = "GitLab did not process the request, and responded with: $gitlabError",
        statusCode = response.code,
        requestUrl = request.url,
        responseBody = body,
      )
    }
  }

  private fun buildPayload(
    pipelineToken: String,
    refName: String,
    run: ProcessableBlockRun,
  ): MultipartBody {
    val payload = mapper.writeValueAsString(run)

    val body = MultipartBody.Builder()
      .setType(MultipartBody.Companion.FORM)
      .addFormDataPart("token", pipelineToken)
      .addFormDataPart("ref", refName)
      .addFormDataPart("variables[MESHSTACK_BEHAVIOR]", run.meshObject.spec.behavior.name)
      .addFormDataPart("variables[MESHSTACK_RUN]", payload)

    // TODO we could think about throwing if we exceed the maximum number of allowed variables here.
    val inputsAsVariable = run.meshObject.spec.buildingBlock.spec.inputs
      .filter { input -> input.isEnvironment }
      .associate { input -> input.key to input.value }

    inputsAsVariable.forEach { (key, value) ->
      body.addFormDataPart("variables[$key]", value.toString())
    }

    // Add the inputs as GL inputs as key value pairs.
    // TODO we could think about throwing if we exceed the maximum number of allowed inputs here.
    val inputsAsInput = run.meshObject.spec.buildingBlock.spec.inputs
      .filter { input -> !input.isEnvironment }
      .associate { input -> input.key to input.value }

    inputsAsInput.forEach { (key, value) ->
      body.addFormDataPart("inputs[$key]", value.toString())
    }

    // Add the callback URLs
    val selfUrl = run.links["self"]?.href
    val registerSourceUrl = run.links["registerSource"]?.href
    val updateSourceUrl = run.links["updateSource"]?.href
    val meshstackBaseUrl = run.links["meshstackBaseUrl"]?.href

    if (selfUrl != null) {
      body.addFormDataPart("variables[MESHSTACK_SELF_URL]", selfUrl)
    } else {
      logMissingUrl("selfUrl", run)
    }

    if (registerSourceUrl != null) {
      body.addFormDataPart("variables[MESHSTACK_REGISTER_SOURCE_URL]", registerSourceUrl)
    } else {
      logMissingUrl("registerSourceUrl", run)
    }

    if (updateSourceUrl != null) {
      body.addFormDataPart("variables[MESHSTACK_UPDATE_SOURCE_URL]", updateSourceUrl)
    } else {
      logMissingUrl("updateSourceUrl", run)
    }

    if (meshstackBaseUrl != null) {
      body.addFormDataPart("variables[MESHSTACK_BASE_URL]", meshstackBaseUrl)
    } else {
      logMissingUrl("meshstackBaseUrl", run)
    }

    return body.build()
  }

  private fun logMissingUrl(
    urlName: String,
    run: ProcessableBlockRun,
  ) {
    log.warn {
      "Could not extract $urlName from run object: ${run.meshObject.meaningfulIdentifier}, " +
        "existing links: ${run.links}"
    }
  }
}
