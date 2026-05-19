package io.meshcloud.meshobjects

import com.fasterxml.jackson.annotation.JsonProperty
import io.meshcloud.meshobjects.exception.InvalidMeshKindException

enum class MeshKind(val value: String) {

  @JsonProperty("meshUser")
  MeshUser("meshUser"),

  @JsonProperty("meshCustomer")
  MeshCustomer("meshCustomer"),

  @JsonProperty("meshWorkspace")
  MeshWorkspace("meshWorkspace"),

  @JsonProperty("meshPaymentMethod")
  MeshPaymentMethod("meshPaymentMethod"),

  @JsonProperty("meshProject")
  MeshProject("meshProject"),

  @JsonProperty("meshCustomerUserBinding")
  MeshCustomerUserBinding("meshCustomerUserBinding"),

  @JsonProperty("meshWorkspaceUserBinding")
  MeshWorkspaceUserBinding("meshWorkspaceUserBinding"),

  @JsonProperty("meshProjectUserBinding")
  MeshProjectUserBinding("meshProjectUserBinding"),

  @JsonProperty("meshProjectGroupBinding")
  MeshProjectGroupBinding("meshProjectGroupBinding"),

  @JsonProperty("meshTenant")
  MeshTenant("meshTenant"),

  @JsonProperty("meshBuildingBlock")
  MeshBuildingBlock("meshBuildingBlock"),

  @JsonProperty("meshBuildingBlockRun")
  MeshBuildingBlockRun("meshBuildingBlockRun"),

  @JsonProperty("meshBuildingBlockRunner")
  MeshBuildingBlockRunner("meshBuildingBlockRunner"),

  @JsonProperty("meshServiceInstance")
  MeshServiceInstance("meshServiceInstance"),

  @JsonProperty("meshServiceInstanceBinding")
  MeshServiceBinding("meshServiceInstanceBinding"),

  @JsonProperty("meshCustomerUserGroup")
  MeshCustomerUserGroup("meshCustomerUserGroup"),

  @JsonProperty("meshWorkspaceUserGroup")
  MeshWorkspaceUserGroup("meshWorkspaceUserGroup"),

  @JsonProperty("meshCustomerGroupBinding")
  MeshCustomerGroupBinding("meshCustomerGroupBinding"),

  @JsonProperty("meshWorkspaceGroupBinding")
  MeshWorkspaceGroupBinding("meshWorkspaceGroupBinding"),

  @JsonProperty("meshChargeback")
  MeshChargeback("meshChargeback"),

  @JsonProperty("meshTenantUsageReport")
  MeshTenantUsageReport("meshTenantUsageReport"),

  @JsonProperty("meshResourceUsageReport")
  MeshResourceUsageReport("meshResourceUsageReport"),

  @JsonProperty("meshExchangeRate")
  MeshExchangeRate("meshExchangeRate"),

  @JsonProperty("meshTagDefinition")
  MeshTagDefinition("meshTagDefinition"),

  @JsonProperty("meshLandingZone")
  MeshLandingZone("meshLandingZone"),

  @JsonProperty("meshPlatform")
  MeshPlatform("meshPlatform"),

  @JsonProperty("meshPlatformType")
  MeshPlatformType("meshPlatformType"),

  @JsonProperty("meshBuildingBlockDefinition")
  MeshBuildingBlockDefinition("meshBuildingBlockDefinition"),

  @JsonProperty("meshProjectRole")
  MeshProjectRole("meshProjectRole"),

  @JsonProperty("meshPolicyDefinition")
  MeshPolicyDefinition("meshPolicyDefinition"),

  @JsonProperty("meshBuildingBlockDefinitionVersion")
  MeshBuildingBlockDefinitionVersion("meshBuildingBlockDefinitionVersion"),

  @JsonProperty("meshCommunicationDefinition")
  MeshCommunicationDefinition("meshCommunicationDefinition"),

  @JsonProperty("meshCommunication")
  MeshCommunication("meshCommunication"),

  @JsonProperty("meshIntegration")
  MeshIntegration("meshIntegration"),

  @JsonProperty("meshLocation")
  MeshLocation("meshLocation"),

  @JsonProperty("meshEventLog")
  MeshEventLog("meshEventLog"),

  @JsonProperty("meshApiKey")
  MeshApiKey("meshApiKey"),

  /**
   * Note: meshPrincipal should not be a meshKind because we never expose the type "meshPrincipal"
   * to our customers. They know about meshUser and meshWorkspaceUserGroup but nothing about meshPrincipal.
   * We need this for completeness reasons to fully integrate the meshPrincipal tag schemas.
   *
   * One central pattern is to handle meshUsers and meshWorkspaceUserGroups in exactly the same way during
   * access control. Therefore, we apply tags and policies at the meshPrincipal level and avoid creating
   * discrepancies between the tag/policy definitions for meshUser and meshWorkspaceUserGroup.
   */
  @JsonProperty("meshPrincipal")
  MeshPrincipal("meshPrincipal");

  override fun toString(): String {
    return value
  }

  companion object {
    fun findKind(data: Map<String, Any>): MeshKind {
      val value = data["kind"] ?: throw InvalidMeshKindException("Kind can't be null or undefined.")

      if (value !is String) {
        throw InvalidMeshKindException("Kind $value does not exist. Please use another one.")
      }

      return fromValue(value)
    }

    fun fromValue(value: String): MeshKind {
      return tryParseFromValue(value)
        ?: throw InvalidMeshKindException("No matching kind for [$value]")
    }

    fun tryParseFromValue(value: String): MeshKind? {
      return entries.singleOrNull { it.value.equals(value, ignoreCase = true) }

    }
  }
}
