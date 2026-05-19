package io.meshcloud.http

import com.fasterxml.jackson.databind.ObjectMapper
import com.fasterxml.jackson.module.kotlin.readValue
import com.fasterxml.jackson.module.kotlin.registerKotlinModule
import mu.KotlinLogging
import java.io.IOException

private val log = KotlinLogging.logger { }

open class RemoteResponseErrorHandler(
  val remoteSystemName: String
) {

  private val mapper = ObjectMapper().registerKotlinModule()

  @Throws(IOException::class)
  open fun handleError(status: HttpStatus, unformattedBody: String): String {
    logResponseBody(status, unformattedBody)

    val statusInfo = getStatusInfo(status)
    val remoteMessage = extractMessageFromBody(unformattedBody, statusInfo)

    return when {
      status.is4xxClientError() -> handleClient4xxErrors(status, remoteMessage)
      status.is5xxServerError() -> handleServer5xxErrors(status, remoteMessage)
      else -> "An error occurred at $remoteSystemName."
    }
  }

  protected open fun handleClient4xxErrors(status: HttpStatus, remoteMessage: String): String {
    return when (status) {
      HttpStatus.NOT_FOUND -> "Resource has not been found in $remoteSystemName!"

      HttpStatus.GONE -> "Resource does not exist in $remoteSystemName anymore!"

      HttpStatus.CONFLICT -> "$remoteSystemName reported a conflict for this request."

      HttpStatus.REQUEST_TIMEOUT -> "Request to $remoteSystemName took to long, there seem to be some networking issues. " +
        "Please try again in a few minutes."

      HttpStatus.UNAUTHORIZED -> "$remoteSystemName declined the request, because authorization was not successful. " +
        "In most cases this is a configuration issue of the backend and an administrator has already been informed " +
        "about the error. But you could also verify, whether you're still logged in correctly " +
        "(i.e. by doing a page reload). "

      HttpStatus.FORBIDDEN -> "You are not allowed to execute this request to $remoteSystemName. This might also be a " +
        "server-side configuration issue, so an admin has been informed about the error."

      else -> "The request made to $remoteSystemName could not be processed."
    }
  }

  protected open fun handleServer5xxErrors(status: HttpStatus, remoteMessage: String): String {
    return when (status) {
      HttpStatus.BAD_GATEWAY,
      HttpStatus.GATEWAY_TIMEOUT -> "Communication with $remoteSystemName is not possible! " +
        "The system is either not available at the moment or not reachable via network. " +
        "An administrator has been informed about this issue. Please try again later!"

      HttpStatus.SERVICE_UNAVAILABLE -> "Communication with $remoteSystemName is currently not possible! " +
        "The system seems to be overloaded or down for maintenance. Please try again later!"

      else -> "An error occurred at $remoteSystemName."
    }
  }

  private fun getStatusInfo(status: HttpStatus) = "${status.value} (${status.reasonPhrase})"

  private fun logResponseBody(status: HttpStatus, body: String) {
    val statusInfo = getStatusInfo(status)
    val message = """An error occurred during communication with $remoteSystemName.
        HTTP Status: $statusInfo,
        Body: $body
    """.trimIndent()

    when (status) {
      HttpStatus.NOT_FOUND, HttpStatus.GONE -> log.info { message }
      else -> log.warn { message }
    }
  }

  private fun isProbablyJson(text: String): Boolean {
    return text.trim().startsWith("{")
  }

  private fun extractMessageFromBody(body: String, statusInfo: String): String {
    if (!isProbablyJson(body)) {
      return bodyOrDefaultMessageIfEmptyBody(body, statusInfo)
    }

    return try {
      var details: Map<String, Any> = mapper.readValue(body)
      if (details["error"] is Map<*, *>) {
        @Suppress("UNCHECKED_CAST")
        details = details["error"] as Map<String, Any>
      }
      details["message"]?.toString()
        ?: details["description"]?.toString()
        ?: details["errorDescription"]?.toString()
        ?: details["error_description"]?.toString()
        ?: details["error"]?.toString()
        ?: details["details"]?.toString()
        ?: bodyOrDefaultMessageIfEmptyBody(body, statusInfo)
    } catch (e: Exception) {
      bodyOrDefaultMessageIfEmptyBody(body, statusInfo)
    }
  }

  private fun bodyOrDefaultMessageIfEmptyBody(body: String, statusInfo: String): String {
    val isEmptyBody = body.trim().replace("[{}]".toRegex(), "").isEmpty()
    return if (isEmptyBody) {
      "HTTP Status $statusInfo - no further details available"
    } else {
      body
    }
  }
}
