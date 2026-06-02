package io.meshcloud.buildingblocks.runner.security

import io.github.oshai.kotlinlogging.KotlinLogging
import java.io.File

private val log = KotlinLogging.logger {}

object PrivateKeyLoader {
  private const val DEFAULT_KEY_FILE = "/app/runner-private.pem"
  private const val ENV_KEY_FILE = "RUNNER_PRIVATE_KEY_FILE"

  fun resolve(privateKeyFile: String, inlinePrivateKey: String): String {
    val filePath = System.getenv(ENV_KEY_FILE)?.takeIf { it.isNotBlank() }
      ?: privateKeyFile.takeIf { it.isNotBlank() }
      ?: DEFAULT_KEY_FILE

    val file = File(filePath)
    if (file.exists()) {
      log.info { "Loaded private key from $filePath" }
      return file.readText()
    }

    return inlinePrivateKey
  }
}
