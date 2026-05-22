package io.meshcloud.buildingblocks.runner.http

import okhttp3.HttpUrl

class MeshHttpException(
  val userMessage: String,
  val systemMessage: String? = null,
  val statusCode: Int,
  val requestUrl: HttpUrl,
  private val responseBody: String,
) : Exception(buildMessage(userMessage, systemMessage, statusCode, requestUrl)) {

  fun getResponseBody(): String = responseBody

  companion object {
    private fun buildMessage(
      userMessage: String,
      systemMessage: String?,
      statusCode: Int,
      requestUrl: HttpUrl,
    ): String = buildString {
      append(userMessage)
      systemMessage?.let { append(" - $it") }
      append(" [HTTP $statusCode $requestUrl]")
    }
  }
}
