package edge

import "gopkg.in/yaml.v3"

// TopLevel returns the value node for a top-level key of a config document, or nil.
func TopLevel(doc *yaml.Node, key string) *yaml.Node {
	if doc == nil {
		return nil
	}
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

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
