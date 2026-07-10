package config

import "gopkg.in/yaml.v2"

// unmarshalToMap decodes raw YAML into a generic map so Load can deep-merge two layers
// before decoding into the caller's typed struct. Empty/nil input decodes to an empty
// map so deepMerge and decodeMap never need a nil special case.
func unmarshalToMap(raw []byte) (map[interface{}]interface{}, error) {
	if len(raw) == 0 {
		return map[interface{}]interface{}{}, nil
	}
	m := map[interface{}]interface{}{}
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m == nil {
		return map[interface{}]interface{}{}, nil
	}
	return m, nil
}

// deepMerge merges src onto dst, key-wise: a key present in src overrides dst; nested
// maps merge recursively; every other value (including slices) is replaced wholesale --
// there is no slice-append merge semantics (a per-impl list fully replaces the base
// list for that key, matching how a plain YAML overlay would read).
func deepMerge(dst, src map[interface{}]interface{}) map[interface{}]interface{} {
	for k, v := range src {
		if srcMap, ok := v.(map[interface{}]interface{}); ok {
			if dstMap, ok := dst[k].(map[interface{}]interface{}); ok {
				dst[k] = deepMerge(dstMap, srcMap)
				continue
			}
		}
		dst[k] = v
	}
	return dst
}

// decodeMap round-trips merged (a map[interface{}]interface{} tree) through YAML
// marshal/unmarshal into `into`, so the caller's typed struct fields (bool/int/string/…)
// are populated by the same yaml.v2 tag-driven decoding every persona already uses --
// one place does typed coercion, not stringly-typed lookups at call sites (D7).
func decodeMap(merged map[interface{}]interface{}, into any) error {
	raw, err := yaml.Marshal(merged)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(raw, into)
}
