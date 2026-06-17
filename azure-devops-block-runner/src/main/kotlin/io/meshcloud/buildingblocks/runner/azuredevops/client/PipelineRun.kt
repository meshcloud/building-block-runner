package io.meshcloud.buildingblocks.runner.azuredevops.client

import com.fasterxml.jackson.annotation.JsonProperty
import com.fasterxml.jackson.annotation.JsonValue

data class PipelineRun(
  val id: Long,
  val name: String?,
  val state: PipelineRunState,
  val result: PipelineRunResult?,
  val createdDate: String,
  val finishedDate: String?,
  val url: String?,
  @JsonProperty("_links")
  val links: Map<String, Map<String, String>>?,
)

enum class PipelineRunResult(
  @JsonValue val value: String,
) {
  UNKNOWN("unknown"),
  SUCCEEDED("succeeded"),
  FAILED("failed"),
  CANCELED("canceled"),
}

enum class PipelineRunState(
  @JsonValue val value: String,
) {
  UNKNOWN("unknown"),
  IN_PROGRESS("inProgress"),
  CANCELING("canceling"),
  COMPLETED("completed"),
}

