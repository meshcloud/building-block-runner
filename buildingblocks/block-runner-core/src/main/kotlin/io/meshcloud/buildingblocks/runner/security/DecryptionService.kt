package io.meshcloud.buildingblocks.runner.security

import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun

interface DecryptionService {
  interface PrivateKeyProvider {
    val privateKey: String
  }

  fun decrypt(secret: String): String

  fun decryptBlockRunInputs(run: ProcessableBlockRun): ProcessableBlockRun
}
