package io.meshcloud.meshobjects.objects

import com.fasterxml.jackson.annotation.JsonIgnore
import com.fasterxml.jackson.annotation.JsonSubTypes
import com.fasterxml.jackson.annotation.JsonTypeInfo
import io.meshcloud.meshobjects.IMeshObject
import io.meshcloud.meshobjects.MeshHalMediaTypes
import io.meshcloud.meshobjects.MeshKind
import io.meshcloud.meshobjects.MeshObject

@MeshObject(
  kind = MeshKind.MeshBuildingBlockRun,
  mediaTypes = [
    MeshHalMediaTypes.MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1,
  ]
)
data class MeshBuildingBlockRun(
  override val apiVersion: String = "v1",
  override val kind: MeshKind = MeshKind.MeshBuildingBlockRun,
  val metadata: MeshBuildingBlockRunMetadata,
  val spec: MeshBuildingBlockRunSpec,
  val status: RunStatus,
) : IMeshObject {

  override val meaningfulIdentifier = "meshBuildingBlockRun[${metadata.uuid}]"

  data class MeshBuildingBlockRunMetadata(
    val uuid: String
  )

  data class BlockRunSourceRegistration(
    val source: SourceRegistration,
    val steps: List<StepRegistration>,
  ) {
    data class SourceRegistration(
      val id: String,
      val externalRunId: String? = null,
      val externalRunUrl: String? = null
    )

    data class StepRegistration(
      val id: String,
      val displayName: String,
      val status: ExecutionStatus? = ExecutionStatus.PENDING,
    )
  }

  enum class RunStatus {
    IN_PROGRESS,
    SUCCEEDED,
    FAILED
  }

  enum class ExecutionStatus(val isTerminalState: Boolean) {
    PENDING(false),
    IN_PROGRESS(false),
    SUCCEEDED(true),
    FAILED(true),
    ABORTED(true)
  }

  data class SourceUpdate(
    val status: ExecutionStatus? = null,
    val steps: List<StepUpdate>? = null,
  ) {
    data class StepUpdate(
      val id: String,
      val displayName: String? = null,
      val userMessage: String? = null,
      val systemMessage: String? = null,
      val outputs: Map<String, BlockRunOutput>? = null,
      val status: ExecutionStatus? = null,
    ) {
      data class BlockRunOutput(
        val value: Any,
        val type: MeshBuildingBlockIOType,
        val isSensitive: Boolean?,
      )

      override fun toString(): String {
        return "StepUpdate(id=$id, userMessage=${userMessage?.take(50)}, systemMessage=${systemMessage?.take(50)}, " +
          "outputs=$outputs, status=$status)"
      }
    }
  }

  data class MeshBuildingBlockRunSpec(
    val runNumber: Long,
    val buildingBlock: BuildingBlock,
    val buildingBlockDefinition: BuildingBlockDefinition,
    val behavior: Behavior,
    val runToken: String
  ) {
    data class BuildingBlock(
      val uuid: String,
      val spec: MeshBuildingBlockSpecForRun
    ) {
      /**
       * Sadly both specs are not compatible as this one needs different input/variable information.
       */
      data class MeshBuildingBlockSpecForRun(
        val displayName: String,
        val workspaceIdentifier: String,
        val projectIdentifier: String?,
        val fullPlatformIdentifier: String?,
        val inputs: List<MeshBuildingBlockInputsForRun>,
        val parentBuildingBlocks: List<MeshParentBuildingBlock>
      )
    }

    enum class Behavior {
      APPLY,
      DETECT,
      DESTROY
    }

    data class MeshBuildingBlockInputsForRun(
      val key: String,
      val value: Any,
      val type: MeshBuildingBlockIOType,
      val isSensitive: Boolean,
      val isEnvironment: Boolean
    )

    data class BuildingBlockDefinition(
      val uuid: String,
      val spec: MeshBuildingBlockMinimalDefinitionSpec
    ) {

      data class MeshBuildingBlockMinimalDefinitionSpec(
        val workspaceIdentifier: String,
        val version: Long,
        val implementation: BuildingBlockImplementation
      )
    }
  }

  @JsonIgnore
  inline fun <reified T : Any> getImplementation(): T {
    if (spec.buildingBlockDefinition.spec.implementation is T) {
      return spec.buildingBlockDefinition.spec.implementation
    } else {
      throw IllegalStateException("The building block implementation of run ${metadata.uuid} was not of expected type.")
    }
  }
}

@JsonTypeInfo(use = JsonTypeInfo.Id.NAME, include = JsonTypeInfo.As.PROPERTY, property = "type")
@JsonSubTypes(
  JsonSubTypes.Type(value = MeshManualBuildingBlockImplementation::class, name = "MANUAL"),
  JsonSubTypes.Type(value = MeshBuildingBlockTerraformImplementation::class, name = "TERRAFORM"),
  JsonSubTypes.Type(value = MeshBuildingBlockGithubImplementation::class, name = "GITHUB_WORKFLOW"),
  JsonSubTypes.Type(value = MeshBuildingBlockGitlabImplementation::class, name = "GITLAB_CICD"),
  JsonSubTypes.Type(value = MeshBuildingBlockAzureDevOpsImplementation::class, name = "AZURE_DEVOPS"),
)
sealed class BuildingBlockImplementation

class MeshManualBuildingBlockImplementation : BuildingBlockImplementation()

data class MeshBuildingBlockTerraformImplementation(
  val terraformVersion: String,
  val repositoryUrl: String,
  val repositoryPath: String?,
  /**
   * Can be null then always the latest is used.
   */
  val refName: String?,
  /**
   * It can be null, if https-url (no ssh)
   */
  val sshPrivateKey: String?,
  val knownHost: KnownHostEntry?,
  val async: Boolean,
  val useMeshHttpBackendFallback: Boolean,
  val preRunScript: String? = null,
) : BuildingBlockImplementation() {

  data class KnownHostEntry(
    val host: String,
    val keyType: String,
    val keyValue: String
  )
}

data class MeshBuildingBlockGithubImplementation(
  // these 4 settings will not be in the BB definition, but rather come from the  source platform
  // (only in case of GitHub Implementation Type)
  val githubBaseUrl: String,
  val owner: String,
  val appId: String,
  val appPem: String,
  val repository: String,
  val branch: String,
  val applyWorkflow: String,
  val destroyWorkflow: String?,
  val async: Boolean,
  /**
   * When true, omits passing the full building block run JSON as input to the GitHub workflow.
   * The workflow will only receive the buildingBlockRunUrl input.
   * This helps avoid GitHub's workflow_dispatch input size limits (65,535 characters total).
   * Default is false for backwards compatibility.
   */
  val omitRunObjectInput: Boolean
) : BuildingBlockImplementation() {

  companion object {
    fun test(
      async: Boolean = false,
      omitRunObjectInput: Boolean = false
    ): MeshBuildingBlockGithubImplementation {
      return MeshBuildingBlockGithubImplementation(
        githubBaseUrl = "https://api.github.com",
        owner = "owner",
        appId = "appId",
        appPem = "appPem",
        repository = "repository",
        branch = "ref",
        applyWorkflow = "provision.yml",
        destroyWorkflow = "deprovision.yml",
        async = async,
        omitRunObjectInput = omitRunObjectInput
      )
    }
  }
}

data class MeshBuildingBlockGitlabImplementation(
  val gitlabBaseUrl: String,
  val projectId: String,
  val refName: String,
  val pipelineTriggerToken: String,
) : BuildingBlockImplementation() {

  companion object {
    fun test(
      pipelineTriggerToken: String = "test123"
    ): MeshBuildingBlockGitlabImplementation {
      return MeshBuildingBlockGitlabImplementation(
        gitlabBaseUrl = "https://gitlab.com",
        projectId = "123456",
        refName = "main",
        pipelineTriggerToken = pipelineTriggerToken
      )
    }
  }
}

data class MeshBuildingBlockAzureDevOpsImplementation(
  val azureDevOpsBaseUrl: String,
  val organization: String,
  val project: String,
  val pipelineId: String,
  val personalAccessToken: String,
  val async: Boolean,
  val refName: String?,
) : BuildingBlockImplementation() {

  companion object {
    fun test(
      personalAccessToken: String = "test_pat",
      async: Boolean = false,
      refName: String? = null
    ): MeshBuildingBlockAzureDevOpsImplementation {
      return MeshBuildingBlockAzureDevOpsImplementation(
        azureDevOpsBaseUrl = "https://dev.azure.com",
        organization = "test-org",
        project = "test-project",
        pipelineId = "123",
        personalAccessToken = personalAccessToken,
        async = async,
        refName = refName
      )
    }
  }
}
