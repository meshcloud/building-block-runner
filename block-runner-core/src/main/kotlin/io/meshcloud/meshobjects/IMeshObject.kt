package io.meshcloud.meshobjects

import com.fasterxml.jackson.annotation.JsonIgnore
import com.fasterxml.jackson.annotation.JsonPropertyOrder

// note: ordering the properties here produces nicer outputs in test, it's not a functional guarantee for our API
@JsonPropertyOrder("kind", "apiVersion", "metadata", "spec", "status")
interface IMeshObject {
  val apiVersion: String
  val kind: MeshKind

  companion object {
    fun formatApiVersion(v: MeshHalMediaTypes.Version): String {
      return "v${v.number}${if (v.preview) "-preview" else ""}"
    }

    // TODO: is there a test to ensure all MeshObjects are ordered correctly by this comparator?
    // Otherwise it's too easy to miss extending this comparator when adding a new meshObject (idea: maybe this should be a property on meshKind)

    // Used for sorting a batch of import objects to make sure the dependencies are in order (Users created before Workspaces etc.).
    // Note: This is in reverse order, the first kind will be imported last.
    val ORDER: Comparator<IMeshObject> = compareBy(
      { it.kind == MeshKind.MeshServiceBinding },
      { it.kind == MeshKind.MeshServiceInstance },
      { it.kind == MeshKind.MeshTenant },
      { it.kind == MeshKind.MeshProjectGroupBinding },
      { it.kind == MeshKind.MeshProjectUserBinding },
      { it.kind == MeshKind.MeshCustomerGroupBinding },
      { it.kind == MeshKind.MeshWorkspaceGroupBinding },
      { it.kind == MeshKind.MeshCustomerUserBinding },
      { it.kind == MeshKind.MeshWorkspaceUserBinding },
      { it.kind == MeshKind.MeshCustomerUserGroup },
      { it.kind == MeshKind.MeshWorkspaceUserGroup },
      { it.kind == MeshKind.MeshProject },
      { it.kind == MeshKind.MeshPaymentMethod },
      { it.kind == MeshKind.MeshCustomer },
      { it.kind == MeshKind.MeshWorkspace },
      { it.kind == MeshKind.MeshUser },
      { it.kind == MeshKind.MeshChargeback },
      { it.kind == MeshKind.MeshTenantUsageReport },
      { it.kind == MeshKind.MeshExchangeRate },
      { it.kind == MeshKind.MeshEventLog },
    )
  }

  /**
   * This contains a human readable hint so you can easily identify
   * this object e.g. in the YAML file it was coming from.
   */
  @get:JsonIgnore
  val meaningfulIdentifier: String
}
