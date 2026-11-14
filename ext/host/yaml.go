package host

import "gopkg.in/yaml.v3"

// DecodePluginConfig fills out from an instance's plugin settings: a value of out's own type (or a pointer to it,
// as a host application's instance source would set) is copied; anything else (e.g. a map from YAML) is converted
// through YAML. A nil value leaves out unchanged.
func DecodePluginConfig[T any](v any, out *T) error {
	switch x := v.(type) {
	case nil:
		return nil
	case T:
		*out = x
		return nil
	case *T:
		if x != nil {
			*out = *x
		}
		return nil
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, out)
}
