package config

import "gopkg.in/yaml.v2"

// unmarshalToMap decodes raw YAML into a generic map so Load can scan it for legacy
// blocks before decoding into the caller's typed struct. Empty/nil input decodes to an
// empty map so decodeMap never needs a nil special case.
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

// decodeMap round-trips merged (a map[interface{}]interface{} tree) through YAML
// marshal/unmarshal into `into`, so the caller's typed struct fields (bool/int/string/…)
// are populated by the same yaml.v2 tag-driven decoding every runner type already uses --
// one place does typed coercion, not stringly-typed lookups at call sites.
func decodeMap(merged map[interface{}]interface{}, into any) error {
	raw, err := yaml.Marshal(merged)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(raw, into)
}
