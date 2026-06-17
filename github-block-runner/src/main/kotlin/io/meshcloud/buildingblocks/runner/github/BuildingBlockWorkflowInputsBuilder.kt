package io.meshcloud.buildingblocks.runner.github

import io.meshcloud.buildingblocks.runner.meshobject.MeshObjectApiObjectMapper
import io.meshcloud.buildingblocks.runner.meshobject.ProcessableBlockRun
import io.meshcloud.meshobjects.objects.MeshBuildingBlockGithubImplementation
import java.util.*

/**
 * Sealed interface for building GitHub workflow dispatch input maps.
 * This is a discriminated union that delegates the responsibility of building
 * the input map to the calling code based on which mode is needed.
 */
sealed interface BuildingBlockWorkflowInputsBuilder {

  companion object {
    /**
     * System input keys that should be passed as workflow inputs when available.
     * These keys match the constants defined in SystemInputGenerator.
     */
    const val MESHSTACK_API_TOKEN_KEY = "MESHSTACK_API_TOKEN"
    const val MESHSTACK_RUN_TOKEN_KEY = "MESHSTACK_RUN_TOKEN"
    const val MESHSTACK_ENDPOINT_KEY = "MESHSTACK_ENDPOINT"
  }

  /**
   * Converts this input variant to a Map<String, String> suitable for the GitHub API.
   */
  fun toInputMap(): Map<String, String>

  /**
   * Pass only the URL to the building block run, along with sensitive system inputs.
   * The workflow must fetch the run data from this URL.
   * This is the preferred modern approach.
   *
   * @param decryptedBlockRun The block run with decrypted inputs (contains links and decrypted tokens)
   */
  class WithUrl(
    decryptedBlockRun: ProcessableBlockRun,
  ) : BuildingBlockWorkflowInputsBuilder {

    val buildingBlockRunUrl: String = decryptedBlockRun.links["self"]?.href
      ?: throw IllegalStateException("No self link found for building block run ${decryptedBlockRun.meshObject.metadata.uuid}")

    private val inputs = decryptedBlockRun.meshObject.spec.buildingBlock.spec.inputs
    private val sensitiveSystemInputKeys = setOf(MESHSTACK_API_TOKEN_KEY, MESHSTACK_RUN_TOKEN_KEY)

    // Extract sensitive system inputs (MESHSTACK_API_TOKEN and MESHSTACK_RUN_TOKEN) from the decrypted block run.
    // These are passed directly as workflow inputs because workflows cannot decrypt them when they fetch the run data.
    // Only the runner has the private key to decrypt these tokens, so we have to extract and pass them here if they are present.
    private val sensitiveInputs: Map<String, String> = inputs
      .filter { it.key in sensitiveSystemInputKeys }
      .associate { it.key to it.value.toString() }

    // Extract MESHSTACK_ENDPOINT if present - it's not sensitive but useful for workflows using the API token
    private val endpointInput: String? = inputs.find { it.key == MESHSTACK_ENDPOINT_KEY }?.value?.toString()

    override fun toInputMap(): Map<String, String> {
      return buildMap {
        put("buildingBlockRunUrl", buildingBlockRunUrl)
        // Add sensitive system inputs if they exist.
        // These will only be used by workflows that expect them - GitHub will reject
        // unexpected inputs, but our error handling will provide a helpful message.
        // But as we only pass them when the according tokens are available as inputs,
        // this should be fine as old workflows that don't expect them also won't have
        // them and thus won't receive them.
        putAll(sensitiveInputs)

        // Include MESHSTACK_ENDPOINT only when MESHSTACK_API_TOKEN is present
        // This ensures the endpoint is available for workflows that need to call the meshStack API
        if (sensitiveInputs.containsKey(MESHSTACK_API_TOKEN_KEY) && endpointInput != null) {
          put(MESHSTACK_ENDPOINT_KEY, endpointInput)
        }
      }
    }
  }

  /**
   * Pass only the full run object as base64-encoded JSON.
   * Legacy mode for workflows that expect the run object directly.
   */
  data class WithRun(
    val buildingBlockRun: ProcessableBlockRun,
  ) : BuildingBlockWorkflowInputsBuilder {

    companion object {
      /**
       * This mapper sanitizes the building block run object by ignoring sensitive fields
       * that we don't want or don't need to pass to GitHub workflows.
       */
      private val sanitizingObjectMapper = MeshObjectApiObjectMapper.mapper
        .copy()
        .addMixIn(
          MeshBuildingBlockGithubImplementation::class.java,
          IgnoreBuildingBlockGithubImplementationMixin::class.java,
        )
    }

    private fun serializeToBase64Json(buildingBlockRun: ProcessableBlockRun): String {
      val json = sanitizingObjectMapper.writeValueAsString(buildingBlockRun)
      val b64 = Base64.getEncoder()

      return b64.encodeToString(json.toByteArray())
    }

    override fun toInputMap(): Map<String, String> {
      return mapOf(
        "buildingBlockRun" to serializeToBase64Json(buildingBlockRun),
      )
    }
  }
}
