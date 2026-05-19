package io.meshcloud.meshobjects

/**
 * This annotation is the source of truth for mapping meshObjects to media types.
 * Because endpoints are also mapped to mediaTypes, this allows us to ensure that endpoints and meshObjects are in sync.
 */
@Target(AnnotationTarget.CLASS)
annotation class MeshObject(
  val kind: MeshKind,
  /**
   * The media types used for this object, typically just one.
   */
  val mediaTypes: Array<String>,
  /**
   * Describes the availability of all endpoints using the mediaTypes of this object.
   */
  val availability: ApiLifecycleState = ApiLifecycleState.GA
) {
  // TODO: I'm not yet 100% happy with this modelling, but it's a bit too early to tell what sort of requirements we have
  // for declaring vs. inferring lifecycle states
  enum class ApiLifecycleState {
    /**
     * Reserved for future use, not implemented yet.
     */
    Preview,

    /**
     * All endpoints using this object should be updated to use the new version.
     * This is the default availability.
     */
    GA,

    /**
     * A new version of the object is available, but the old version is still supported.
     */
    Outdated,

    /**
     * The endpoint is officially deprecated. You don't need to set this, this is also detected based on the presence
     * of a @MeshObjectPlannedDeprecation annotation.
     */
    Deprecated,


    /**
     * Set this value when the object is removed from the API.
     * TODO: consider whether we only want to annotate a retired meshobject or so also remove the model from the codebase
     */
    Retired
  }
}

