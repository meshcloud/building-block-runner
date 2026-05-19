package io.meshcloud.meshobjects.objects

data class MeshBuildingBlockIO(
  val key: String,
  val value: Any?,
  val valueType: MeshBuildingBlockIOType,
)

enum class MeshBuildingBlockIOType {
  STRING,
  CODE,
  INTEGER,
  BOOLEAN,
  FILE,
  LIST, // only until user permissions are migrate to CODE
  SINGLE_SELECT,
  MULTI_SELECT,
}
