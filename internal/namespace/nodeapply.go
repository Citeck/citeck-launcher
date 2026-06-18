package namespace

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// nodeapply.go — apply a data delta onto a YAML document while preserving key
// order (native to yaml.Node) and grafting the user's comments back by key.

// applyDeltaToYAML parses templateYAML into a yaml.Node, applies the data delta
// (RFC 7386 semantics: null deletes; arrays/scalars replace), grafts comments
// from userYAML (the on-disk user file) onto matching keys, and re-encodes.
// userYAML may be nil/empty (no comment source).
func applyDeltaToYAML(templateYAML []byte, delta any, userYAML []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(templateYAML, &doc); err != nil {
		return nil, fmt.Errorf("parse template node: %w", err)
	}
	root := docRoot(&doc)
	if root == nil { // empty template → synthesize a mapping
		root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	}
	applyPatchToNode(root, delta)
	// The generated baseline for an empty webapp cloud-config is the flow-style
	// empty mapping "{}" (generator_webapp.go). A flow mapping keeps its flow
	// style when keys are appended, so a user editing such a file would get the
	// whole document collapsed onto one line ("{wqeqwe: [{dsds: sda}]}") — it
	// reads like JSON. Drop flow style on the (now-populated) root so it
	// re-encodes as conventional block YAML.
	if root.Kind == yaml.MappingNode && root.Style&yaml.FlowStyle != 0 && len(root.Content) > 0 {
		root.Style = 0
	}
	if len(userYAML) > 0 {
		var udoc yaml.Node
		if yaml.Unmarshal(userYAML, &udoc) == nil {
			if uroot := docRoot(&udoc); uroot != nil {
				graftComments(root, uroot)
			}
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("encode node: %w", err)
	}
	_ = enc.Close()
	return buf.Bytes(), nil
}

func docRoot(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		return n.Content[0]
	}
	return n
}

// applyPatchToNode overlays a data delta onto a mapping node in place.
func applyPatchToNode(node *yaml.Node, patch any) {
	patchObj, ok := patch.(map[string]any)
	if !ok || node.Kind != yaml.MappingNode {
		return
	}
	for key, pv := range patchObj {
		idx := mappingKeyIndex(node, key)
		if pv == nil { // delete
			if idx >= 0 {
				node.Content = append(node.Content[:idx], node.Content[idx+2:]...)
			}
			continue
		}
		if sub, isObj := pv.(map[string]any); isObj {
			if idx >= 0 && node.Content[idx+1].Kind == yaml.MappingNode {
				applyPatchToNode(node.Content[idx+1], sub)
				continue
			}
		}
		valNode := anyToNode(pv)
		if idx >= 0 {
			node.Content[idx+1] = valNode
		} else {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, valNode)
		}
	}
}

// mappingKeyIndex returns the index of the KEY node for `key`, or -1. The value
// node is at index+1 (yaml mapping Content is [k0,v0,k1,v1,...]).
func mappingKeyIndex(node *yaml.Node, key string) int {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// anyToNode converts a decoded value to a yaml.Node via round-trip marshal.
func anyToNode(v any) *yaml.Node {
	var n yaml.Node
	_ = n.Encode(v)
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return &n
}

// graftComments copies Head/Line/Foot comments from src onto dst for matching
// mapping keys, recursing into matching mapping values. src (the user file) is
// authoritative: any comment present in src overwrites dst's; keys absent in src
// keep dst's (template) comments.
func graftComments(dst, src *yaml.Node) {
	if dst == nil || src == nil || dst.Kind != yaml.MappingNode || src.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(src.Content); i += 2 {
		key := src.Content[i].Value
		j := mappingKeyIndex(dst, key)
		if j < 0 {
			continue
		}
		copyComments(dst.Content[j], src.Content[i])     // key node
		copyComments(dst.Content[j+1], src.Content[i+1]) // value node
		graftComments(dst.Content[j+1], src.Content[i+1])
	}
}

func copyComments(dst, src *yaml.Node) {
	if src.HeadComment != "" {
		dst.HeadComment = src.HeadComment
	}
	if src.LineComment != "" {
		dst.LineComment = src.LineComment
	}
	if src.FootComment != "" {
		dst.FootComment = src.FootComment
	}
}
