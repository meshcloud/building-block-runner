package io.meshcloud.exception

import kotlin.contracts.ExperimentalContracts
import kotlin.contracts.contract

fun requireOrThrowsSystem(value: Boolean, lazyMessage: () -> String) {
  if (!value) {
    throw MeshSystemException(lazyMessage())
  }
}

fun requireOrThrowsMesh(value: Boolean, ex: () -> MeshException) {
  if (!value) {
    throw ex()
  }
}

@OptIn(ExperimentalContracts::class)
inline fun <T : Any> requireNotNullOrThrowSystem(
  value: T?,
  lazyMessage: () -> String = { "Required value was null." }
): T {
  contract {
    returns() implies (value != null)
  }
  if (value == null) {
    throw MeshSystemException(lazyMessage())
  }
  return value
}
