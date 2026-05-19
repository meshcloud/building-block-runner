package io.meshcloud.buildingblocks.runner.github

import com.fasterxml.jackson.annotation.JsonProperty
import com.fasterxml.jackson.core.JsonParser
import com.fasterxml.jackson.databind.DeserializationContext
import com.fasterxml.jackson.databind.DeserializationFeature
import com.fasterxml.jackson.databind.JsonDeserializer
import com.fasterxml.jackson.databind.annotation.JsonDeserialize
import com.fasterxml.jackson.datatype.jdk8.Jdk8Module
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule
import com.fasterxml.jackson.module.kotlin.jacksonObjectMapper
import com.fasterxml.jackson.module.kotlin.registerKotlinModule
import io.meshcloud.exception.MeshException
import io.meshcloud.http.*
import io.meshcloud.http.MediaTypes.MEDIA_TYPE_JSON
import io.meshcloud.meshobjects.MeshHalMediaTypes.MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1
import mu.KotlinLogging
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody


private val log = KotlinLogging.logger { }

class GithubClient(
  private val githubApiBaseUrl: String,
) {

  sealed interface TriggerWorkflowResult {
    data object Success : TriggerWorkflowResult

    data class UnsupportedInput(val unsupportedInputNames: Set<String>, val responseBody: String) : TriggerWorkflowResult

    data class Error(val statusCode: Int, val responseBody: String) : TriggerWorkflowResult
  }

  enum class WorkflowRunStatus(val value: String) {
    QUEUED("queued"),
    IN_PROGRESS("in_progress"),
    COMPLETED("completed");

    companion object {
      fun fromString(value: String): WorkflowRunStatus {
        return entries.find { it.value == value }
          ?: throw IllegalArgumentException("Unknown workflow run status: $value")
      }
    }
  }

  enum class WorkflowJobStatus(val value: String) {
    QUEUED("queued"),
    IN_PROGRESS("in_progress"),
    COMPLETED("completed");

    companion object {
      fun fromString(value: String): WorkflowJobStatus {
        return entries.find { it.value == value }
          ?: throw IllegalArgumentException("Unknown workflow job status: $value")
      }
    }
  }

  class WorkflowRunStatusDeserializer : JsonDeserializer<WorkflowRunStatus>() {
    override fun deserialize(p: JsonParser, ctxt: DeserializationContext): WorkflowRunStatus {
      return WorkflowRunStatus.fromString(p.valueAsString)
    }
  }

  class WorkflowJobStatusDeserializer : JsonDeserializer<WorkflowJobStatus>() {
    override fun deserialize(p: JsonParser, ctxt: DeserializationContext): WorkflowJobStatus {
      return WorkflowJobStatus.fromString(p.valueAsString)
    }
  }

  data class DispatchWorkflowPayload(
    val ref: String,
    val inputs: Map<String, String>
  )

  private data class AppInstallation(
    @JsonProperty("id")
    val installationId: String,
    @JsonProperty("app_id")
    val appId: String,
    @JsonProperty("client_id")
    val clientId: String,
    @JsonProperty("target_type")
    val targetType: String,
  )

  private data class InstallationToken(
    val token: String,
    @JsonProperty("expires_at")
    val expiresAt: String,
    val permissions: Map<String, String>,
    @JsonProperty("repository_selection")
    val repositorySelection: String
  )

  data class WorkflowRun(
    val id: Long,
    @JsonDeserialize(using = WorkflowRunStatusDeserializer::class)
    val status: WorkflowRunStatus,
    val conclusion: String?,
    @JsonProperty("created_at")
    val createdAt: String,
    @JsonProperty("updated_at")
    val updatedAt: String,
    @JsonProperty("html_url")
    val htmlUrl: String
  )

  data class WorkflowJob(
    val id: Long,
    val name: String,
    @JsonDeserialize(using = WorkflowJobStatusDeserializer::class)
    val status: WorkflowJobStatus,
    val conclusion: String?,
    @JsonProperty("started_at")
    val startedAt: String?,
    @JsonProperty("completed_at")
    val completedAt: String?,
    @JsonProperty("html_url")
    val htmlUrl: String
  )

  private data class WorkflowRunsResponse(
    @JsonProperty("workflow_runs")
    val workflowRuns: List<WorkflowRun>
  )

  private data class WorkflowJobsResponse(
    val jobs: List<WorkflowJob>
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
   * This fetches a token to authenticate against the
   * GitHub API for an app installation.
   */
  fun getInstallationAuthToken(appAuthToken: String, installationId: String): String {
    val baseUrl = "$githubApiBaseUrl/app/installations/".toHttpUrl()
    val url = baseUrl.newBuilder()
      .addPathSegment(installationId)
      .addPathSegment("access_tokens")
      .build()

    val request = Request.Builder()
      .url(url)
      .post(EMPTY_REQUEST_BODY)
      .addHeader("Accept", " application/vnd.github+json")
      .addHeader("X-GitHub-Api-Version", "2022-11-28")
      .addHeader("Authorization", "Bearer $appAuthToken")
      .build()

    return client.execute<String>(request)
      .letWithStatus(HttpStatus.CREATED) {
        val installationAuthToken = mapper.readValue(it.body, InstallationToken::class.java)
        validateInstallationToken(installationAuthToken)
        it.handled(installationAuthToken.token)
      }.getContent()
  }

  private fun validateInstallationToken(installationAuthToken: InstallationToken) {
    val permissions = installationAuthToken.permissions

    // We require 'metadata' and 'actions=write' for our GitHub integration
    // We do not check for 'metadata' as that is a mandatory permission for any GitHub App.
    if (permissions["actions"] != "write") {
      throw MeshException(
        "Your installed GitHub App is missing write permissions for actions. " +
          "Required permissions: actions=write. Actual permissions: $permissions"
      )
    }
  }

  fun getInstallationId(appAuthToken: String, owner: String, repositoryName: String): String {
    val baseUrl = "$githubApiBaseUrl/repos/".toHttpUrl()
    val url = baseUrl.newBuilder()
      .addPathSegment(owner)
      .addPathSegment(repositoryName)
      .addPathSegment("installation")
      .build()

    val request = Request.Builder()
      .url(url)
      .get()
      .addHeader("Accept", " application/vnd.github+json")
      .addHeader("X-GitHub-Api-Version", "2022-11-28")
      .addHeader("Authorization", "Bearer $appAuthToken")
      .build()

    return client.execute<String>(request)
      .letWithStatus(HttpStatus.OK) {
        val installation = mapper.readValue(it.body, AppInstallation::class.java)
        it.handled(installation.installationId)
      }.getContent()
  }

  fun triggerWorkflow(
    installationAuthToken: String,
    owner: String,
    repositoryName: String,
    workflowName: String,
    payload: DispatchWorkflowPayload,
    recognizedUnsupportedInputs: Set<String> = emptySet()
  ): TriggerWorkflowResult {
    val url = "$githubApiBaseUrl/repos/$owner/$repositoryName/actions/workflows/$workflowName/dispatches".toHttpUrl()
    val payloadBody = mapper.writeValueAsString(payload)
    val request = buildTriggerRequest(url, payloadBody, installationAuthToken)

    return client.execute<TriggerWorkflowResult>(request)
      .letIfSuccess {
        it.handled(TriggerWorkflowResult.Success)
      }
      .letWithStatus(HttpStatus.UNPROCESSABLE_ENTITY) {
        val foundUnsupportedInputs = recognizedUnsupportedInputs.filter { inputName ->
          isUnsupportedInputError(it.body, inputName)
        }.toSet()

        if (foundUnsupportedInputs.isNotEmpty()) {
          it.handled(TriggerWorkflowResult.UnsupportedInput(foundUnsupportedInputs, it.body))
        } else {
          it.handled(TriggerWorkflowResult.Error(it.status.value, it.body))
        }
      }
      .letIfError { error ->
        error.handled(TriggerWorkflowResult.Error(error.status.value, error.body))
      }
      .getContent()
  }

  private fun isUnsupportedInputError(responseContent: String, inputName: String): Boolean {
    return responseContent.contains("Unexpected inputs provided") &&
      responseContent.contains(inputName)
  }

  private fun buildTriggerRequest(url: HttpUrl, payload: String, token: String): Request {
    return Request.Builder()
      .url(url)
      .post(payload.toRequestBody(MEDIA_TYPE_JSON))
      .addHeader("Content-Type", MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1)
      .addHeader("Accept", " application/vnd.github+json")
      .addHeader("X-GitHub-Api-Version", "2022-11-28")
      .addHeader("Authorization", "Bearer $token")
      .build()
  }

  /**
   * Lists workflow runs for the specified workflow file, ordered by creation date (newest first).
   */
  fun listWorkflowRuns(
    installationAuthToken: String,
    owner: String,
    repositoryName: String,
    workflowName: String,
    perPage: Int = 10
  ): List<WorkflowRun> {
    val url = "$githubApiBaseUrl/repos/$owner/$repositoryName/actions/workflows/$workflowName/runs".toHttpUrl()
      .newBuilder()
      .addQueryParameter("per_page", perPage.toString())
      .build()

    val request = Request.Builder()
      .url(url)
      .get()
      .addHeader("Accept", "application/vnd.github+json")
      .addHeader("X-GitHub-Api-Version", "2022-11-28")
      .addHeader("Authorization", "Bearer $installationAuthToken")
      .build()

    return client.execute<List<WorkflowRun>>(request)
      .letWithStatus(HttpStatus.OK) {
        val response = mapper.readValue(it.body, WorkflowRunsResponse::class.java)
        it.handled(response.workflowRuns)
      }.getContent()
  }

  /**
   * Gets a specific workflow run by ID.
   */
  fun getWorkflowRun(
    installationAuthToken: String,
    owner: String,
    repositoryName: String,
    runId: Long
  ): WorkflowRun {
    val url = "$githubApiBaseUrl/repos/$owner/$repositoryName/actions/runs/$runId".toHttpUrl()

    val request = Request.Builder()
      .url(url)
      .get()
      .addHeader("Accept", " application/vnd.github+json")
      .addHeader("X-GitHub-Api-Version", "2022-11-28")
      .addHeader("Authorization", "Bearer $installationAuthToken")
      .build()

    return client.execute<WorkflowRun>(request)
      .letWithStatus(HttpStatus.OK) {
        val workflowRun = mapper.readValue(it.body, WorkflowRun::class.java)
        it.handled(workflowRun)
      }.getContent()
  }

  /**
   * Lists all jobs for a workflow run.
   */
  fun listWorkflowJobs(
    installationAuthToken: String,
    owner: String,
    repositoryName: String,
    runId: Long
  ): List<WorkflowJob> {
    val url = "$githubApiBaseUrl/repos/$owner/$repositoryName/actions/runs/$runId/jobs".toHttpUrl()

    val request = Request.Builder()
      .url(url)
      .get()
      .addHeader("Accept", " application/vnd.github+json")
      .addHeader("X-GitHub-Api-Version", "2022-11-28")
      .addHeader("Authorization", "Bearer $installationAuthToken")
      .build()

    return client.execute<List<WorkflowJob>>(request)
      .letWithStatus(HttpStatus.OK) {
        val response = mapper.readValue(it.body, WorkflowJobsResponse::class.java)
        it.handled(response.jobs)
      }.getContent()
  }
}
