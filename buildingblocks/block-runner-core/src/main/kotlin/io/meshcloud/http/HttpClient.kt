package io.meshcloud.http

import com.fasterxml.jackson.core.type.TypeReference
import com.fasterxml.jackson.databind.DeserializationFeature
import com.fasterxml.jackson.databind.ObjectMapper
import com.fasterxml.jackson.dataformat.yaml.YAMLFactory
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule
import com.fasterxml.jackson.module.kotlin.KotlinModule
import com.fasterxml.jackson.module.kotlin.readValue
import com.fasterxml.jackson.module.kotlin.registerKotlinModule
import io.meshcloud.http.MediaTypes.MEDIA_TYPE_FORM
import io.meshcloud.http.MediaTypes.MEDIA_TYPE_JSON
import io.meshcloud.http.MediaTypes.MEDIA_TYPE_YAML
import io.meshcloud.http.exception.MeshHttpException
import mu.KotlinLogging
import okhttp3.*
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody

/**
 * This class is deprecated. It has drawbacks esp. when handling response bodies during a failed response and
 * also not very good logging capabilities.
 * Please use only the OkHttpClient extentions when implementing new http clients. This class would be marked
 * @Deprecated if this would not break our build.
 *
 * Check the new techniques in [OkHttpClientExtention]
 */
open class HttpClient(
  val errorHandler: RemoteResponseErrorHandler,
  val client: OkHttpClient
) {

  private data class CustomErrorMessage(
    val message: String
  )

  data class ExpectedStatus(
    val expectedStatus: Set<HttpStatus>
  )

  protected val log = KotlinLogging.logger { }

  fun Request.Builder.addAuthHeader(header: String): Request.Builder {
    addHeader("Authorization", header)
    return this
  }

  /**
   * Configures a custom error message to include in error messages on unexpected response status codes.
   */
  fun Request.Builder.customErrorMessage(msg: String): Request.Builder {
    tag(CustomErrorMessage::class.java, CustomErrorMessage(msg))
    return this
  }

  /**
   * Configures expected [HttpStatus] response codes that the consumer wants to handle itself.
   * This disables some default response handling.
   */
  fun Request.Builder.expectedStatus(vararg stati: HttpStatus): Request.Builder {
    tag(ExpectedStatus::class.java, ExpectedStatus(stati.toSet()))
    return this
  }

  fun String.toUrlBuilder(): HttpUrl.Builder {
    return this.toHttpUrlOrNull()?.newBuilder()
      ?: throw IllegalStateException("'$this' was not a valid URL")
  }

  inline fun <reified T> Request.execute(typeReference: TypeReference<T>? = null): HttpResponse<T> {
    return client
      .newCall(this)
      .execute()
      .use { response ->
        val httpStatus = HttpStatus.valueOf(response.code)
        val contentAsString = response.body?.string() ?: ""

        // a response status is either expected because it's explicitly registered as an expected code
        // when no explicit handling was registered, we use "expected by default" code (2xx or 3xx code)
        val isExpected = this.tag(ExpectedStatus::class.java)?.expectedStatus?.contains(httpStatus)
          ?: httpStatus.is2xxSuccessful() || httpStatus.is3xxRedirection()

        /**
         * It's important we only log and throw on unexpected errors. We must avoid creating error logs for status
         * codes that represent perfectly fine responses in some contexts (e.g. 409 CONFLICT) so that we can
         * maintain a sane signal-to-noise ratio in our logs.
         */
        if (!isExpected) {
          throwUnexpectedResponseError(this, httpStatus, contentAsString)
        }

        val (responseBody, errorContent) = when {
          contentAsString.isEmpty() -> {
            null to null
          }

          httpStatus.is4xxClientError() || httpStatus.is5xxServerError() -> {
            null to contentAsString
          }

          else -> {
            // if this improves replication robustness make type reference non optional.
            when (typeReference) {
              null -> objectJsonMapper.readValue<T>(contentAsString) to null
              else -> objectJsonMapper.readValue(contentAsString, typeReference) to null
            }
          }
        }

        val responseHeaders = response.headers.toMap()

        HttpResponse(
          status = httpStatus,
          responseHeaders = responseHeaders,
          responseBody = responseBody,
          errorContentAsString = errorContent,
          // todo: that might not be a perfect design but it does the job of making the HttpResponse powerful
          //   enough so that consumers can handle their own response error management on it
          throwUnexpected = { throwUnexpectedResponseError(this, httpStatus, contentAsString) }
        )
      }
  }

  fun throwUnexpectedResponseError(request: Request, httpStatus: HttpStatus, contentAsString: String) {
    val customErrorMessage = request.tag(CustomErrorMessage::class.java)?.message
    val baseMessage = customErrorMessage?.let { "$it. " } ?: ""

    // it's typically a 4xx error but in any case it's not server related
    val message = baseMessage + errorHandler.handleError(httpStatus, contentAsString)

    throw MeshHttpException(
      systemMessage = message,
      userMessage = message,
      response = HttpResponseFailed<Any>(
        request = request,
        body = contentAsString,
        responseHeaders = emptyMap(),
        status = httpStatus
      )
    )
  }

  fun Headers.toMap(): Map<String, Any> = names().map { it to (get(it) ?: "") }.toMap()

  companion object {

    val objectJsonMapper = ObjectMapper()
      .apply {
        registerKotlinModule()
        registerModules(JavaTimeModule())
        configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false)
        enable(DeserializationFeature.USE_BIG_DECIMAL_FOR_FLOATS)
      }

    private val objectYmlMapper = ObjectMapper(YAMLFactory())
      .apply {
        registerKotlinModule()
        registerModule(JavaTimeModule())
        configure(DeserializationFeature.FAIL_ON_UNKNOWN_PROPERTIES, false)
        enable(DeserializationFeature.USE_BIG_DECIMAL_FOR_FLOATS)
      }

    fun toRequestBody(obj: Any, type: MediaType): RequestBody {
      return when (type) {
        MEDIA_TYPE_JSON -> objectJsonMapper
          .writeValueAsString(obj)
          .toRequestBody(MEDIA_TYPE_JSON)

        MEDIA_TYPE_YAML -> objectYmlMapper
          .writeValueAsString(obj)
          .toRequestBody(MEDIA_TYPE_YAML)

        else -> throw IllegalStateException("Unknown Media Type (only YAML and JSON is supported): $type")
      }
    }

    fun toRequestBody(obj: Map<String, String>, type: MediaType): RequestBody {
      return when (type) {
        MEDIA_TYPE_FORM -> {
          val builder = FormBody.Builder()
          obj.forEach { (key, value) -> builder.addEncoded(key, value) }

          return builder.build()
        }

        else -> {
          toRequestBody(obj as Any, type)
        }
      }
    }
  }
}
