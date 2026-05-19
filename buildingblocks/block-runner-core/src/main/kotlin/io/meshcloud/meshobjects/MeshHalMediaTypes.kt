package io.meshcloud.meshobjects

import org.springframework.http.MediaType
import java.lang.reflect.Modifier
import java.util.regex.Pattern

// note: needs to be outside of MeshHalMediaTypes because there's some reflection magic going on over
// this class for media type converions by Spring MVC
private const val prefix = "application/vnd.meshcloud.api"

/**
 * All custom Media Types that are used for Hal responses have to be defined in this object!
 * All members of this object are picked up by [MeshHalMediaTypeConfiguration] via reflection to configure
 * Spring HATEOAS accordingly. Therefore, this object must only contain the available media types.
 */
object MeshHalMediaTypes {

  /**
   * Note: do not use this anymore, use the versioned media types instead.
   * Using the API with latest header is deprecated and will be removed in the future.
   */
  const val MESHOBJECT_MEDIA_TYPE_LATEST = "*/*"

  /**
   * THis media type is used by the SCIM API root object
   * TODO: verify whether we should have this media type at all, as the RFC seems to use application/scim+json
   */
  const val SCIM_API_MEDIA_TYPE_V2 = "$prefix.scim.v2.hal+json"

  /**
   * This media type is used by the API root object
   */
  const val API_MEDIA_TYPE_V1 = "$prefix.v1.hal+json"

  const val METADATA_MEDIA_TYPE_V1 = "$prefix.metadata.v1.hal+json"

  /**
   * These media types are used by meshObject import.
   * There are alternative non-hal versions declared in [MediaTypes] too
   * TODO: unify these two declarations into a single place?
   */
  const val MESHOBJECT_MEDIA_TYPE_V1 = "$prefix.meshobjects.v1.hal+json"
  const val MESHOBJECT_MEDIA_TYPE_V2 = "$prefix.meshobjects.v2.hal+json"

  const val MESHBUILDINGBLOCKDEFINITION_MEDIA_TYPE_V1PREVIEW = "$prefix.meshbuildingblockdefinition.v1-preview.hal+json"
  const val MESHBUILDINGBLOCKDEFINITIONVERSION_MEDIA_TYPE_V1PREVIEW = "$prefix.meshbuildingblockdefinitionversion.v1-preview.hal+json"

  const val MESHBUILDINGBLOCK_MEDIA_TYPE_V1 = "$prefix.meshbuildingblock.v1.hal+json"
  const val MESHBUILDINGBLOCK_MEDIA_TYPE_V2PREVIEW = "$prefix.meshbuildingblock.v2-preview.hal+json"

  const val MESHBUILDINGBLOCKRUN_MEDIA_TYPE_V1 = "$prefix.meshbuildingblockrun.v1.hal+json"

  const val MESHBUILDINGBLOCKRUNNER_MEDIA_TYPE_V1PREVIEW = "$prefix.meshbuildingblockrunner.v1-preview.hal+json"

  const val MESHCHARGEBACK_MEDIA_TYPE_V1 = "$prefix.meshchargeback.v1.hal+json"
  const val MESHCHARGEBACK_MEDIA_TYPE_V2 = "$prefix.meshchargeback.v2.hal+json"
  const val MESHCHARGEBACK_MEDIA_TYPE_V3 = "$prefix.meshchargeback.v3.hal+json"

  const val MESHCUSTOMERGROUPBINDING_MEDIA_TYPE_V1 = "$prefix.meshcustomergroupbinding.v1.hal+json"

  const val MESHCUSTOMER_MEDIA_TYPE_V1 = "$prefix.meshcustomer.v1.hal+json"

  const val MESHCUSTOMERUSERBINDING_MEDIA_TYPE_V1 = "$prefix.meshcustomeruserbinding.v1.hal+json"

  const val MESHCUSTOMERUSERGROUP_MEDIA_TYPE_V1 = "$prefix.meshcustomerusergroup.v1.hal+json"

  const val MESHEXCHANGERATE_MEDIA_TYPE_V1 = "$prefix.meshexchangerate.v1.hal+json"

  const val MESHLANDINGZONE_MEDIA_TYPE_V1 = "$prefix.meshlandingzone.v1.hal+json"
  const val MESHLANDINGZONE_MEDIA_TYPE_V1PREVIEW = "$prefix.meshlandingzone.v1-preview.hal+json"

  const val MESHPAYMENTMETHOD_MEDIA_TYPE_V1 = "$prefix.meshpaymentmethod.v1.hal+json"
  const val MESHPAYMENTMETHOD_MEDIA_TYPE_V2 = "$prefix.meshpaymentmethod.v2.hal+json"

  const val MESHPROJECTGROUPBINDING_MEDIA_TYPE_V1 = "$prefix.meshprojectgroupbinding.v1.hal+json"
  const val MESHPROJECTGROUPBINDING_MEDIA_TYPE_V2 = "$prefix.meshprojectgroupbinding.v2.hal+json"
  const val MESHPROJECTGROUPBINDING_MEDIA_TYPE_V3 = "$prefix.meshprojectgroupbinding.v3.hal+json"

  const val MESHPROJECTUSERBINDING_MEDIA_TYPE_V1 = "$prefix.meshprojectuserbinding.v1.hal+json"
  const val MESHPROJECTUSERBINDING_MEDIA_TYPE_V2 = "$prefix.meshprojectuserbinding.v2.hal+json"
  const val MESHPROJECTUSERBINDING_MEDIA_TYPE_V3 = "$prefix.meshprojectuserbinding.v3.hal+json"

  const val MESHPROJECT_MEDIA_TYPE_V1 = "$prefix.meshproject.v1.hal+json"
  const val MESHPROJECT_MEDIA_TYPE_V2 = "$prefix.meshproject.v2.hal+json"

  const val MESHPLATFORM_MEDIA_TYPE_V1 = "$prefix.meshplatform.v1.hal+json"
  const val MESHPLATFORM_MEDIA_TYPE_V2 = "$prefix.meshplatform.v2.hal+json"
  const val MESHPLATFORM_MEDIA_TYPE_V2PREVIEW = "$prefix.meshplatform.v2-preview.hal+json"

  const val MESHPLATFORMTYPE_MEDIA_TYPE_V1 = "$prefix.meshplatformtype.v1.hal+json"
  const val MESHPLATFORMTYPE_MEDIA_TYPE_V1PREVIEW = "$prefix.meshplatformtype.v1-preview.hal+json"

  const val MESHLOCATION_MEDIA_TYPE_V1 = "$prefix.meshlocation.v1.hal+json"
  const val MESHLOCATION_MEDIA_TYPE_V1PREVIEW = "$prefix.meshlocation.v1-preview.hal+json"

  /**
   * Beware this type is not used in our API yet. The API lives on kraken-api atm anduses
   * Content-Type: application/vnd.meshcloud.api.meshobjects.v1+json;charset=UTF-8 instead -> which is ugly probably?
   * It is definitely inconsistent with how meshfed-api handles accept headers and requires them to be versioned
   */
  const val MESHRESOURCEUSAGEREPORT_MEDIA_TYPE_V1 = "$prefix.meshresourceusagereport.v1.hal+json"

  const val MESHSERVICEBINDING_MEDIA_TYPE_V1 = "$prefix.meshservicebinding.v1.hal+json"

  const val MESHSERVICEINSTANCE_MEDIA_TYPE_V1 = "$prefix.meshserviceinstance.v1.hal+json"
  const val MESHSERVICEINSTANCE_MEDIA_TYPE_V2 = "$prefix.meshserviceinstance.v2.hal+json"

  const val MESHTAGDEFINITION_MEDIA_TYPE_V1 = "$prefix.meshtagdefinition.v1.hal+json"

  const val MESHTENANTUSAGEREPORT_MEDIA_TYPE_V1 = "$prefix.meshtenantusagereport.v1.hal+json"
  const val MESHTENANTUSAGEREPORT_MEDIA_TYPE_V2 = "$prefix.meshtenantusagereport.v2.hal+json"
  const val MESHTENANTUSAGEREPORT_MEDIA_TYPE_V3 = "$prefix.meshtenantusagereport.v3.hal+json"

  const val MESHTENANT_MEDIA_TYPE_V1 = "$prefix.meshtenant.v1.hal+json"
  const val MESHTENANT_MEDIA_TYPE_V2 = "$prefix.meshtenant.v2.hal+json"
  const val MESHTENANT_MEDIA_TYPE_V3 = "$prefix.meshtenant.v3.hal+json"
  const val MESHTENANT_MEDIA_TYPE_V4PREVIEW = "$prefix.meshtenant.v4-preview.hal+json"

  const val MESHUSER_MEDIA_TYPE_V1 = "$prefix.meshuser.v1.hal+json"
  const val MESHUSER_MEDIA_TYPE_V2 = "$prefix.meshuser.v2.hal+json"

  const val MESHWORKSPACE_MEDIA_TYPE_V1 = "$prefix.meshworkspace.v1.hal+json"
  const val MESHWORKSPACE_MEDIA_TYPE_V2 = "$prefix.meshworkspace.v2.hal+json"

  const val MESHPROJECTROLE_MEDIA_TYPE_V1 = "$prefix.meshprojectrole.v1.hal+json"

  const val MESHWORKSPACEUSERGROUP_MEDIA_TYPE_V1 = "$prefix.meshworkspaceusergroup.v1.hal+json"

  const val MESHWORKSPACEGROUPBINDING_MEDIA_TYPE_V1 = "$prefix.meshworkspacegroupbinding.v1.hal+json"
  const val MESHWORKSPACEGROUPBINDING_MEDIA_TYPE_V2 = "$prefix.meshworkspacegroupbinding.v2.hal+json"

  const val MESHWORKSPACEUSERBINDING_MEDIA_TYPE_V1 = "$prefix.meshworkspaceuserbinding.v1.hal+json"
  const val MESHWORKSPACEUSERBINDING_MEDIA_TYPE_V2 = "$prefix.meshworkspaceuserbinding.v2.hal+json"

  const val MESHAPIKEY_MEDIA_TYPE_V1PREVIEW = "$prefix.meshapikey.v1-preview.hal+json"

  private val versionRegex = Pattern.compile("""$prefix.*\.v(\d+)(-preview)?(\.hal)?""")

  const val MESHCOMMUNICATIONDEFINITION_MEDIA_TYPE_V1PREVIEW = "$prefix.meshcommunicationdefinition.v1-preview.hal+json"
  const val MESHCOMMUNICATION_MEDIA_TYPE_V1PREVIEW = "$prefix.meshcommunication.v1-preview.hal+json"

  const val MESHEVENTLOG_MEDIA_TYPE_V1PREVIEW = "$prefix.mesheventlog.v1-preview.hal+json"

  const val MESHINTEGRATION_MEDIA_TYPE_V1 = "$prefix.meshintegration.v1.hal+json"
  const val MESHINTEGRATION_MEDIA_TYPE_V1PREVIEW = "$prefix.meshintegration.v1-preview.hal+json"

  // the problem is that we need sorting (which would require semver sorting probably)
  // atm. we only have numeric versions, but we may have v1-beta or similar in the future too
  fun tryExtractVersion(mediaType: String): Version? {
    val matchResult = versionRegex.matcher(mediaType)
    return if (matchResult.find()) {
      val number = matchResult.group(1)?.toIntOrNull()
      val preview = matchResult.group(2) == "-preview"
      number?.let { Version(number = it, preview = preview) }
    } else {
      null
    }
  }

  val knownMediaTypes = (extractStringConstants(MeshHalMediaTypes) + extractStringConstants(MediaTypes)).toSet()

  val knownMediaTypesToVersion = knownMediaTypes.associateWith { tryExtractVersion(it) }

  private fun extractStringConstants(obj: Any): List<String> {
    return obj::class.java.fields
      .filter {
        it.type == String::class.java && Modifier.isPublic(it.modifiers) &&
          Modifier.isFinal(it.modifiers) && Modifier.isStatic(it.modifiers)
      }
      .map { field -> field.get(null) as String }
  }

  /**
   * Attempts to find a known MeshHalMediaType for the given media type.
   *
   * This matching is done by comparing the type and subtype of the media type, ignoring any charset or other parameters.
   */
  fun fromMediaType(mediaType: MediaType): String? {
    val canonicalType = "${mediaType.type}/${mediaType.subtype}"

    val isKnown = knownMediaTypes.contains(canonicalType)

    return if (isKnown) {
      canonicalType
    } else {
      null
    }
  }

  data class Version(
    val number: Int,
    val preview: Boolean = false,
  ) {
    override fun toString(): String {
      return "v$number${if (preview) "-preview" else ""}"
    }
  }
}
