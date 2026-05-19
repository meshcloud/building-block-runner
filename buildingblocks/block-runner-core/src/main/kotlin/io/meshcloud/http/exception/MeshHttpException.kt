package io.meshcloud.http.exception

import io.meshcloud.exception.MeshException
import io.meshcloud.http.*
import okhttp3.Headers
import okhttp3.Request
import java.util.*

/**
 * This is the Exception which is preferred from the modern HttpClient setup together with OkHttp.
 */
open class MeshHttpException : MeshException {

  val response: HttpResponseBase<*>

  constructor(
    userMessage: String,
    systemMessage: String,
    response: HttpResponseBase<*>,
    cause: Throwable? = null
  ) : super(
    userMessage = userMessage,
    // This is a bit special here. We must make sure to include the response information so it gets placed
    // correctly in the original exception and thus logged as logger seem not to call toString()
    systemMessage = "$systemMessage\n- Request: ${response.request.method} ${response.request.url}\n- Response: ${response.status.value} ${response.status.name} ${
      HttpResponseBase.truncateBodyForLog(
        response.body
      )
    }",
    cause = cause
  ) {
    // note: We sanitize this response here, because it's very easy to accidentally leak sensitive
    // information from this exception, e.g. when putting info from the exception into a log statement.
    // we could get red of this is we didn't have to include the response as a field on the exception at all,
    // unfortunately we do still have a few places where this is used (as of 04/24 e.g. in Azure Cost Management retry logic)
    this.response = when (response) {
      is HttpResponseFailed -> response.copy(
        responseHeaders = removeSensitiveHeaders(response.responseHeaders).toMultimap(),
        request = removeSensitiveHeaders(response)
      )

      is HttpResponseSuccess -> response.copy(
        responseHeaders = removeSensitiveHeaders(response.responseHeaders).toMultimap(),
        request = removeSensitiveHeaders(response)
      )

      is HttpResponseProcessed -> response.copy(
        responseHeaders = removeSensitiveHeaders(response.responseHeaders).toMultimap(),
        request = removeSensitiveHeaders(response)
      )
    }
  }

  constructor(
    userMessage: String,
    response: HttpResponseBase<*>,
    cause: Throwable? = null
  ) : this(systemMessage = userMessage, userMessage = userMessage, response = response, cause = cause)

  fun matchesStatus(status: HttpStatus): Boolean {
    return response.status == status
  }

  fun getResponseBody(): String {
    return response.body
  }

  /**
   * Note: Many loggers do not call Exception.toString when logging an exception but instead format the log line
   * using Exception.message. It's therefore important to pass the message into the base class ctor and don't
   * rely on .toString()
   */
  override fun toString(): String {
    return """
      ${this.javaClass.name}: Response: $response
      systemMessage: $systemMessage, userMessage: $userMessage
      """.trimIndent()
  }

  companion object {
    /**
     * Sensitive headers that shall be removed from log messages
     */
    private val sensitiveHeaders = setOf("Authorization")
      .map { invariantHeaderKey(it) }
      .toSet()

    private fun removeSensitiveHeaders(headers: Map<String, List<String>>): Headers {
      val result = Headers.Builder()

      headers.forEach { (k, vs) ->
        if (sensitiveHeaders.contains(invariantHeaderKey(k))) {
          result.add(k, "***")
        } else {
          vs.forEach {
            result.add(k, it)
          }
        }
      }

      return result.build()
    }

    private fun invariantHeaderKey(k: String): String {
      return k.lowercase(Locale.US)
    }

    fun removeSensitiveHeaders(response: HttpResponseBase<*>): Request {
      val sanitizedHeaders = removeSensitiveHeaders(response.request.headers.toMultimap())

      return response.request
        .newBuilder()
        .headers(sanitizedHeaders)
        .build()
    }
  }
}
