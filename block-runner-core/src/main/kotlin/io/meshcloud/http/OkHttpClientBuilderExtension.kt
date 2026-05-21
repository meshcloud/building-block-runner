package io.meshcloud.http

import mu.KLogger
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor
import java.security.SecureRandom
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import javax.net.ssl.HostnameVerifier
import javax.net.ssl.SSLContext
import javax.net.ssl.X509TrustManager

// see: https://stackoverflow.com/questions/25509296/trusting-all-certificates-with-okhttp
fun OkHttpClient.Builder.skipSslValidation(skipSslValidation: Boolean): OkHttpClient.Builder = apply {
  if (!skipSslValidation) {
    return this
  }

  val trustManager = object : X509TrustManager {
    @Throws(CertificateException::class)
    override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) {
    }

    @Throws(java.security.cert.CertificateException::class)
    override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {
    }

    override fun getAcceptedIssuers(): Array<X509Certificate> {
      return arrayOf()
    }
  }

  val sslContext = SSLContext.getInstance("SSL")
  sslContext.init(null, arrayOf(trustManager), SecureRandom())
  val sslSocketFactory = sslContext.socketFactory

  this.sslSocketFactory(sslSocketFactory, trustManager)
  this.hostnameVerifier(HostnameVerifier { _, _ -> true })
}

fun OkHttpClient.Builder.addLogging(log: KLogger): OkHttpClient.Builder = apply {
  if (log.underlyingLogger.isTraceEnabled) {
    val loggingInterceptor = HttpLoggingInterceptor().apply {
      redactHeader("Authorization")
      redactHeader("Cookie")
      level = HttpLoggingInterceptor.Level.BODY
    }
    addInterceptor(loggingInterceptor)
  }
}
