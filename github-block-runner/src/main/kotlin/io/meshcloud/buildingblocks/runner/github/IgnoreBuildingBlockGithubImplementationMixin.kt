package io.meshcloud.buildingblocks.runner.github

import com.fasterxml.jackson.annotation.JsonIgnore

abstract class IgnoreBuildingBlockGithubImplementationMixin {

  @get:JsonIgnore
  abstract val type: String

  @get:JsonIgnore
  abstract val githubBaseUrl: String

  @get:JsonIgnore
  abstract val owner: String

  @get:JsonIgnore
  abstract val appId: String

  @get:JsonIgnore
  abstract val appPem: String

  @get:JsonIgnore
  abstract val repository: String

  @get:JsonIgnore
  abstract val branch: String

  @get:JsonIgnore
  abstract val applyWorkflow: String

  @get:JsonIgnore
  abstract val destroyWorkflow: String?

  @get:JsonIgnore
  abstract val async: Boolean

  @get:JsonIgnore
  abstract val omitRunObjectInput: Boolean
}
