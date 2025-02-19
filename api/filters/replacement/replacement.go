// Copyright 2021 The Kubernetes Authors.
// SPDX-License-Identifier: Apache-2.0

package replacement

import (
	"fmt"
	"strings"

	"sigs.k8s.io/kustomize/api/resid"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

type Filter struct {
	Replacements []types.Replacement `json:"replacements,omitempty" yaml:"replacements,omitempty"`
}

// Filter replaces values of targets with values from sources
func (f Filter) Filter(nodes []*yaml.RNode) ([]*yaml.RNode, error) {
	for _, r := range f.Replacements {
		if r.Source == nil || r.Targets == nil {
			return nil, fmt.Errorf("replacements must specify a source and at least one target")
		}
		value, err := getReplacement(nodes, &r)
		if err != nil {
			return nil, err
		}
		nodes, err = applyReplacement(nodes, value, r.Targets)
		if err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

func applyReplacement(nodes []*yaml.RNode, value *yaml.RNode, targets []*types.TargetSelector) ([]*yaml.RNode, error) {
	for _, t := range targets {
		if t.Select == nil {
			return nil, fmt.Errorf("target must specify resources to select")
		}
		if len(t.FieldPaths) == 0 {
			t.FieldPaths = []string{types.DefaultReplacementFieldPath}
		}
		for _, n := range nodes {
			nodeId := getKrmId(n)
			if t.Select.KrmId.Match(nodeId) && !rejectId(t.Reject, nodeId) {
				err := applyToNode(n, value, t)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return nodes, nil
}

func rejectId(rejects []*types.Selector, nodeId *types.KrmId) bool {
	for _, r := range rejects {
		if r.KrmId.Match(nodeId) {
			return true
		}
	}
	return false
}

func applyToNode(node *yaml.RNode, value *yaml.RNode, target *types.TargetSelector) error {
	for _, fp := range target.FieldPaths {
		fieldPath := strings.Split(fp, ".")
		var t *yaml.RNode
		var err error
		if target.Options != nil && target.Options.Create {
			t, err = node.Pipe(yaml.LookupCreate(value.YNode().Kind, fieldPath...))
		} else {
			t, err = node.Pipe(yaml.Lookup(fieldPath...))
		}
		if err != nil {
			return err
		}
		if t != nil {
			if err = setTargetValue(target.Options, t, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func setTargetValue(options *types.FieldOptions, t *yaml.RNode, value *yaml.RNode) error {
	if options != nil && options.Delimiter != "" {

		if t.YNode().Kind != yaml.ScalarNode {
			return fmt.Errorf("delimiter option can only be used with scalar nodes")
		}

		tv := strings.Split(t.YNode().Value, options.Delimiter)
		v := yaml.GetValue(value)
		// TODO: Add a way to remove an element
		switch {
		case options.Index < 0: // prefix
			tv = append([]string{v}, tv...)
		case options.Index >= len(tv): // suffix
			tv = append(tv, v)
		default: // replace an element
			tv[options.Index] = v
		}
		value.YNode().Value = strings.Join(tv, options.Delimiter)
	}
	t.SetYNode(value.YNode())
	return nil
}

func getReplacement(nodes []*yaml.RNode, r *types.Replacement) (*yaml.RNode, error) {
	source, err := selectSourceNode(nodes, r.Source)
	if err != nil {
		return nil, err
	}

	if r.Source.FieldPath == "" {
		r.Source.FieldPath = types.DefaultReplacementFieldPath
	}
	fieldPath := strings.Split(r.Source.FieldPath, ".")

	rn, err := source.Pipe(yaml.Lookup(fieldPath...))
	if err != nil {
		return nil, err
	}
	if !rn.IsNilOrEmpty() {
		return getRefinedValue(r.Source.Options, rn)
	}
	return rn, nil
}

func getRefinedValue(options *types.FieldOptions, rn *yaml.RNode) (*yaml.RNode, error) {
	if options == nil || options.Delimiter == "" {
		return rn, nil
	}
	if rn.YNode().Kind != yaml.ScalarNode {
		return nil, fmt.Errorf("delimiter option can only be used with scalar nodes")
	}
	value := strings.Split(yaml.GetValue(rn), options.Delimiter)
	if options.Index >= len(value) || options.Index < 0 {
		return nil, fmt.Errorf("options.index %d is out of bounds for value %s", options.Index, yaml.GetValue(rn))
	}
	n := rn.Copy()
	n.YNode().Value = value[options.Index]
	return n, nil
}

// selectSourceNode finds the node that matches the selector, returning
// an error if multiple or none are found
func selectSourceNode(nodes []*yaml.RNode, selector *types.SourceSelector) (*yaml.RNode, error) {
	var matches []*yaml.RNode
	for _, n := range nodes {
		if selector.KrmId.Match(getKrmId(n)) {
			if len(matches) > 0 {
				return nil, fmt.Errorf("more than one match for source %v", selector)
			}
			matches = append(matches, n)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("found no matches for source %v", selector)
	}
	return matches[0], nil
}

func getKrmId(n *yaml.RNode) *types.KrmId {
	ns, err := n.GetNamespace()
	if err != nil {
		// Resource has no metadata (no apiVersion, kind, nor metadata field).
		// In this case, it cannot be selected.
		return &types.KrmId{}
	}
	apiVersion := n.Field(yaml.APIVersionField)
	var group, version string
	if apiVersion != nil {
		group, version = resid.ParseGroupVersion(yaml.GetValue(apiVersion.Value))
	}
	return &types.KrmId{
		Gvk:       resid.Gvk{Group: group, Version: version, Kind: n.GetKind()},
		Name:      n.GetName(),
		Namespace: ns,
	}
}
