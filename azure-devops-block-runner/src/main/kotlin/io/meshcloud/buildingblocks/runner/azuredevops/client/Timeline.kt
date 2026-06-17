package io.meshcloud.buildingblocks.runner.azuredevops.client

import com.fasterxml.jackson.annotation.JsonValue

data class TimelineResponse(
  val records: List<TimelineRecord> = emptyList(),
)

data class TimelineRecord(
  val id: String,
  val name: String?,
  val type: TimelineRecordType,
  val state: TimelineRecordState?,
  val result: TimelineRecordResult?,
  val startTime: String?,
  val finishTime: String?,
  val parentId: String?,
  val order: Int,
)

enum class TimelineRecordType(
  @JsonValue val value: String,
) {
  STAGE("Stage"),
  PHASE("Phase"),
  JOB("Job"),
  TASK("Task"),
  CHECKPOINT("Checkpoint"),
  UNKNOWN("Unknown"),
}

enum class TimelineRecordState(
  @JsonValue val value: String,
) {
  PENDING("pending"),
  IN_PROGRESS("inProgress"),
  COMPLETED("completed"),
}

enum class TimelineRecordResult(
  @JsonValue val value: String,
) {
  SUCCEEDED("succeeded"),
  SUCCEEDED_WITH_ISSUES("succeededWithIssues"),
  FAILED("failed"),
  CANCELED("canceled"),
  SKIPPED("skipped"),
  ABANDONED("abandoned"),
}
