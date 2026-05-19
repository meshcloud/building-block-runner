package io.meshcloud.buildingblocks.runner.security

import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import org.springframework.context.annotation.Profile
import org.springframework.stereotype.Service

/**
 * Fallback decryption service that performs no decryption.
 * This is used when the kubernetes profile is active, where the run-controller handles decryption.
 */
@Service
@Profile("kubernetes")
class NoOpDecryptionService : DecryptionService {
  override fun decrypt(secret: String): String {
    return secret
  }

  override fun decryptBlockRunInputs(run: ProcessableBlockRun): ProcessableBlockRun {
    return run
  }

}
