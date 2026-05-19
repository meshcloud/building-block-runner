package io.meshcloud.http

import com.fasterxml.jackson.databind.JsonMappingException
import com.github.tomakehurst.wiremock.WireMockServer
import com.github.tomakehurst.wiremock.client.WireMock.*
import com.github.tomakehurst.wiremock.core.WireMockConfiguration.wireMockConfig
import io.meshcloud.http.exception.MeshHttpException
import io.mockk.confirmVerified
import io.mockk.mockk
import io.mockk.verify
import okhttp3.OkHttpClient
import okhttp3.Request
import org.junit.AfterClass
import org.junit.Assert
import org.junit.Assert.assertThrows
import org.junit.BeforeClass
import org.junit.Test

class HttpClientTest {

  private val remoteSystemName = "remote-system-test-name"
  private val errorHandler = RemoteResponseErrorHandler(remoteSystemName)
  private val httpClient = OkHttpClient.Builder().build()

  @Test
  fun `request with header`() {
    stubFor(
      get(urlEqualTo(PATH))
        .willReturn(
          aResponse()
            .withBody("{\"name\": \"test123\"}")
            .withHeader("test-header", "test-header-value")
        )
    )

    HttpClient(errorHandler, httpClient)
      .apply {
        val response = Request.Builder()
          .url(TEST_URL)
          .build()
          .execute<TestResponse>()

        Assert.assertTrue(response.responseHeaders.containsKey("test-header"))
      }
  }

  data class ApiError(val x: String)

  @Test
  fun `request doesn't invoke error handler until throwUnexpected invoked on the response`() {
    stubFor(
      get(PATH)
        .willReturn(
          aResponse()
            .withBody("")
            .withStatus(HttpStatus.BAD_REQUEST.value)
        )
    )

    val errorHandler = mockk<RemoteResponseErrorHandler>(relaxed = true) {}

    val response = HttpClient(errorHandler, httpClient).run {
      Request.Builder()
        .url(TEST_URL)
        .expectedStatus(HttpStatus.BAD_REQUEST)
        .build()
        .execute<TestResponse>()
    }

    confirmVerified(errorHandler)

    // but throwing does
    assertThrows(MeshHttpException::class.java) {
      response.throwUnexpectedResponse()
    }

    verify(exactly = 1) { errorHandler.handleError(HttpStatus.BAD_REQUEST, "") }
  }

  @Test
  fun `when status is not expected, throws MeshHttpException`() {
    stubFor(
      get(PATH)
        .willReturn(
          aResponse()
            .withBody("""{ "x": 1, "y": 2 }""")
            .withStatus(HttpStatus.ACCEPTED.value)
        )
    )

    assertThrows(MeshHttpException::class.java) {
      HttpClient(errorHandler, httpClient).run {
        Request.Builder()
          .url(TEST_URL)
          .expectedStatus(HttpStatus.OK)
          .build()
          .execute<TestResponse>()
      }
    }
  }


  @Test
  fun `request with expected status allows throw `() {
    stubFor(
      get(PATH)
        .willReturn(
          aResponse()
            .withBody("""{ "x": "y" }""")
            .withStatus(404)
        )
    )

    val response = HttpClient(errorHandler, httpClient).run {
      Request.Builder()
        .url(TEST_URL)
        .addAuthHeader("12345")
        .expectedStatus(HttpStatus.NOT_FOUND)
        .build()
        .execute<TestResponse>()
    }

    Assert.assertEquals(HttpStatus.NOT_FOUND, response.status)
    Assert.assertEquals(null, response.responseBody)
    val error = response.readErrorResponse<ApiError>()
    Assert.assertEquals(error, ApiError("y"))
  }

  @Test
  fun `throws 4xx RemoteClientException`() {
    stubFor(
      get(urlEqualTo(PATH))
        .willReturn(
          aResponse()
            .withBody("{}")
            .withStatus(400)
        )
    )

    val expectedMessage = listOf(
      "The request made to $remoteSystemName could not be processed.",
      "- Request: GET http://localhost:8044/test",
      "- Response: 400 BAD_REQUEST {}"
    ).joinToString("\n")

    var message = ""
    var status = HttpStatus.I_AM_A_TEAPOT

    HttpClient(errorHandler, httpClient)
      .apply {
        assertThrows(MeshHttpException::class.java) {
          Request.Builder()
            .url(TEST_URL)
            .build()
            .execute<Any>()
        }.runCatching {
          message = this.systemMessage
          status = this.response.status
        }
      }

    Assert.assertEquals(expectedMessage, message)
    Assert.assertEquals(HttpStatus.BAD_REQUEST, status)
  }

  @Test
  fun `throws 4xx RemoteClientException with custom message`() {
    stubFor(
      get(urlEqualTo(PATH))
        .willReturn(
          aResponse()
            .withBody("""{"message": "My actual error message!"}""")
            .withStatus(400)
        )
    )

    val customErrorMessage = "Custom error message"

    var message = ""
    var status = HttpStatus.I_AM_A_TEAPOT

    HttpClient(errorHandler, httpClient)
      .apply {
        assertThrows(MeshHttpException::class.java) {
          Request.Builder()
            .url(TEST_URL)
            .customErrorMessage(customErrorMessage)
            .build()
            .execute<Any>()
        }.runCatching {
          message = this.systemMessage
          status = this.response.status
        }
      }

    val expectedMessage = listOf(
      "$customErrorMessage. The request made to $remoteSystemName could not be processed.",
      "- Request: GET http://localhost:8044/test",
      "- Response: 400 BAD_REQUEST {\"message\": \"My actual error message!\"}"
    ).joinToString("\n")

    // make sure the error handler was applied to have the response body, etc in the logs. This can be checked by
    // making sure the user facing error message is added to the exception message
    Assert.assertEquals(expectedMessage, message)
    Assert.assertEquals(HttpStatus.BAD_REQUEST, status)
  }

  @Test
  fun `throws 5xx RemoteServerException`() {
    stubFor(
      get(urlEqualTo(PATH))
        .willReturn(
          aResponse().withBody("{}")
            .withStatus(500)
        )
    )

    val expectedMessage = listOf(
      "An error occurred at $remoteSystemName.",
      "- Request: GET http://localhost:8044/test",
      "- Response: 500 INTERNAL_SERVER_ERROR {}",
    ).joinToString("\n")

    var message = ""
    var status = HttpStatus.I_AM_A_TEAPOT

    HttpClient(errorHandler, httpClient)
      .apply {
        assertThrows(MeshHttpException::class.java) {
          Request.Builder()
            .url(TEST_URL)
            .build()
            .execute<Any>()
        }.runCatching {
          message = this.systemMessage
          status = this.response.status
        }
      }

    Assert.assertEquals(expectedMessage, message)
    Assert.assertEquals(HttpStatus.INTERNAL_SERVER_ERROR, status)
  }

  @Test
  fun `throws 5xx RemoteServerException with custom message`() {
    stubFor(
      get(urlEqualTo(PATH))
        .willReturn(
          aResponse()
            .withBody("""{"message": "My actual error message!"}""")
            .withStatus(503)
        )
    )

    val customErrorMessage = "Custom error message"

    var message = ""
    var status = HttpStatus.I_AM_A_TEAPOT

    HttpClient(errorHandler, httpClient)
      .apply {
        assertThrows(MeshHttpException::class.java) {
          Request.Builder()
            .url(TEST_URL)
            .customErrorMessage(customErrorMessage)
            .build()
            .execute<Any>()
        }.runCatching {
          message = this.systemMessage
          status = this.response.status
        }
      }


    val expectedMessage = listOf(
      "$customErrorMessage. Communication with $remoteSystemName is currently not possible! The system seems to be overloaded or down for maintenance. Please try again later!",
      "- Request: GET http://localhost:8044/test",
      """- Response: 503 SERVICE_UNAVAILABLE {"message": "My actual error message!"}"""
    ).joinToString("\n")

    // make sure the error handler was applied to have the response body, etc in the logs. This can be checked by
    // making sure the user facing error message is added to the exception message
    Assert.assertEquals(expectedMessage, message)
    Assert.assertEquals(HttpStatus.SERVICE_UNAVAILABLE, status)
  }

  @Test
  fun `throws JsonMappingException if response was invalid`() {
    stubFor(
      get(urlEqualTo(PATH))
        .willReturn(
          aResponse()
            .withBody("{\"name\": \"test123\"}")
        )
    )

    HttpClient(errorHandler, httpClient)
      .apply {
        assertThrows(JsonMappingException::class.java) {
          Request.Builder()
            .url(TEST_URL)
            .build()
            .execute<ForceJsonMappingExceptionResponse>()
        }
      }
  }

  @Test
  fun `GET request`() {
    stubFor(
      get(urlEqualTo("$PATH?page=0"))
        .willReturn(
          aResponse()
            .withBody("{}")
        )
    )

    HttpClient(errorHandler, httpClient)
      .apply {
        val url = TEST_URL.toUrlBuilder()
          .addQueryParameter("page", "0")
          .build()
        Request.Builder()
          .url(url)
          .addAuthHeader("12345")
          .build()
          .execute<Any>()
      }
  }

  @Test
  fun `POST request`() {
    stubFor(
      post(urlEqualTo(PATH))
        // TODO the original header contains a charset. Sadly OkHttp seems to be buggy in the regard that this
        //  can not be set. UTF-8 should be the default then. So hopefully all upstream server will understand it.
        //  See: https://github.com/square/okhttp/issues/4393 maybe change it back as soon as this is fixed.
        // .withHeader("Content-Type", equalTo("application/x-www-form-urlencoded; charset=UTF-8"))
        .withHeader("Content-Type", equalTo("application/x-www-form-urlencoded"))
        .withRequestBody(equalTo("name=testName"))
        .willReturn(
          aResponse()
            .withBody("{}")
        )
    )

    val content = mapOf("name" to "testName")

    HttpClient(errorHandler, httpClient)
      .apply {
        val body = HttpClient.toRequestBody(content, MediaTypes.MEDIA_TYPE_FORM)
        Request.Builder()
          .url(TEST_URL)
          .post(body)
          .build()
          .execute<Any>()
      }
  }

  @Test
  fun `PUT request`() {
    stubFor(
      put(urlEqualTo(PATH))
        .willReturn(
          aResponse()
            .withBody("{}")
        )
    )

    val content = mapOf("name" to "testName")

    HttpClient(errorHandler, httpClient)
      .apply {
        Request.Builder()
          .put(HttpClient.toRequestBody(content, MediaTypes.MEDIA_TYPE_JSON))
          .url(TEST_URL)
          .build()
          .execute<Any>()
      }
  }

  @Test
  fun `DELETE request`() {
    stubFor(
      delete(urlEqualTo(PATH))
        .willReturn(
          aResponse()
            .withBody("")
        )
    )

    HttpClient(errorHandler, httpClient)
      .apply {
        Request.Builder()
          .delete()
          .url(TEST_URL)
          .build()
          .execute<Any>()
      }
  }

  /*
    Note:
      We use the HttpClient in the following way:
          HttpClient(errorHandler)
            .apply {
              ...
            }
       The reason is that extension function inside classes can be only called within apply { ... }
       (see: https://stackoverflow.com/questions/50702478/testing-extension-functions-inside-classes).
   */
  companion object {
    private lateinit var wireMockServer: WireMockServer

    @BeforeClass
    @JvmStatic
    fun startServer() {
      wireMockServer = WireMockServer(wireMockConfig().port(PORT))
      wireMockServer.start()
      configureFor("localhost", PORT)
    }

    @AfterClass
    @JvmStatic
    fun stopServer() = wireMockServer.stop()

    private const val PORT = 8044
    const val PATH = "/test"
    const val TEST_URL = "http://localhost:$PORT$PATH"
  }
}

data class TestResponse(val name: String)

data class ForceJsonMappingExceptionResponse(val color: String)
