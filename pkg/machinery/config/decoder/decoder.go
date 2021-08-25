// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package decoder

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	yaml "gopkg.in/yaml.v3"

	"github.com/talos-systems/talos/pkg/machinery/config"
	"github.com/talos-systems/talos/pkg/machinery/config/encoder"
)

var (
	// ErrMissingVersion indicates that the manifest is missing a version.
	ErrMissingVersion = errors.New("missing version")
	// ErrMissingKind indicates that the manifest is missing a kind.
	ErrMissingKind = errors.New("missing kind")
	// ErrMissingSpec indicates that the manifest is missing a spec.
	ErrMissingSpec = errors.New("missing spec")
	// ErrMissingSpecConent indicates that the manifest spec is empty.
	ErrMissingSpecConent = errors.New("missing spec content")
)

const (
	// ManifestVersionKey is the string indicating a manifest's version.
	ManifestVersionKey = "version"
	// ManifestKindKey is the string indicating a manifest's kind.
	ManifestKindKey = "kind"
	// ManifestSpecKey is represents a manifest's spec.
	ManifestSpecKey = "spec"
	// ManifestDeprecatedKey is represents the deprected v1alpha1 manifest.
	ManifestDeprecatedKey = "machine"
)

// Decoder represents a multi-doc YAML decoder.
type Decoder struct {
	source []byte
}

// Decode decodes all known manifests.
func (d *Decoder) Decode() ([]interface{}, error) {
	return d.decode()
}

// NewDecoder initializes and returns a `Decoder`.
func NewDecoder(source []byte) *Decoder {
	return &Decoder{
		source: source,
	}
}

func (d *Decoder) decode() ([]interface{}, error) {
	return parse(d.source)
}

func parse(source []byte) (decoded []interface{}, err error) {
	decoded = []interface{}{}

	r := bytes.NewReader(source)

	dec := yaml.NewDecoder(r)

	dec.KnownFields(true)

	// Iterate through all defined documents.
	for {
		var manifests yaml.Node

		if err = dec.Decode(&manifests); err != nil {
			if errors.Is(err, io.EOF) {
				return decoded, nil
			}

			return nil, fmt.Errorf("decode error: %w", err)
		}

		if manifests.Kind != yaml.DocumentNode {
			return nil, fmt.Errorf("expected a document")
		}

		for _, manifest := range manifests.Content {
			var target interface{}

			if target, err = decode(manifest); err != nil {
				return nil, err
			}

			decoded = append(decoded, target)
		}
	}
}

//nolint:gocyclo,cyclop
func decode(manifest *yaml.Node) (target interface{}, err error) {
	var (
		version string
		kind    string
		spec    *yaml.Node
	)

	for i, node := range manifest.Content {
		switch node.Value {
		case ManifestKindKey:
			if len(manifest.Content) < i+1 {
				return nil, fmt.Errorf("missing manifest content")
			}

			if err = manifest.Content[i+1].Decode(&kind); err != nil {
				return nil, fmt.Errorf("kind decode: %w", err)
			}
		case ManifestVersionKey:
			if len(manifest.Content) < i+1 {
				return nil, fmt.Errorf("missing manifest content")
			}

			if err = manifest.Content[i+1].Decode(&version); err != nil {
				return nil, fmt.Errorf("version decode: %w", err)
			}
		case ManifestSpecKey:
			if len(manifest.Content) < i+1 {
				return nil, fmt.Errorf("missing manifest content")
			}

			spec = manifest.Content[i+1]
		case ManifestDeprecatedKey:
			if target, err = config.New("v1alpha1", ""); err != nil {
				return nil, fmt.Errorf("new deprecated config: %w", err)
			}

			if err = manifest.Decode(target); err != nil {
				return nil, fmt.Errorf("deprecated decode: %w", err)
			}

			if err = validate(target, manifest); err != nil {
				return nil, err
			}

			return target, nil
		}
	}

	if kind == "" {
		return nil, ErrMissingKind
	}

	if version == "" {
		return nil, ErrMissingVersion
	}

	if spec == nil {
		return nil, ErrMissingSpec
	}

	if spec.Content == nil {
		return nil, ErrMissingSpecConent
	}

	if target, err = config.New(kind, version); err != nil {
		return nil, fmt.Errorf("new config: %w", err)
	}

	if err = spec.Decode(target); err != nil {
		return nil, fmt.Errorf("spec decode: %w", err)
	}

	if err = validate(target, spec); err != nil {
		return nil, err
	}

	return target, nil
}

//nolint:gocyclo
func validate(target interface{}, spec *yaml.Node) error {
	node, err := encoder.NewEncoder(target, encoder.WithOmitEmpty(false)).Marshal()
	if err != nil {
		return err
	}

	src := map[string]interface{}{}
	dst := map[string]interface{}{}

	err = spec.Decode(src)
	if err != nil {
		return err
	}

	err = node.Decode(dst)
	if err != nil {
		return err
	}

	var checkUnknown func(interface{}, interface{}) interface{}

	checkUnknown = func(left interface{}, right interface{}) interface{} {
		switch v := left.(type) {
		case map[string]interface{}:
			r, ok := right.(map[string]interface{})
			if !ok {
				return "type mismatch"
			}

			unknownKeys := map[string]interface{}{}

			for key, value := range v {
				if _, ok := r[key]; !ok {
					unknownKeys[key] = value

					continue
				}

				if d := checkUnknown(value, r[key]); d != nil {
					unknownKeys[key] = d
				}
			}

			if len(unknownKeys) > 0 {
				return unknownKeys
			}
		case []interface{}:
			r, ok := right.([]interface{})
			if !ok {
				return "type mismatch"
			}

			if len(v) != len(r) {
				return "slice length differs"
			}

			var unknownItems []interface{}

			for i, item := range v {
				if d := checkUnknown(item, r[i]); d != nil {
					unknownItems = append(unknownItems, d)
				}
			}

			if len(unknownItems) > 0 {
				return unknownItems
			}
		}

		return nil
	}

	diff := checkUnknown(src, dst)
	if diff != nil {
		var data []byte

		if data, err = yaml.Marshal(diff); err != nil {
			return fmt.Errorf("failed to marshal error summary %w", err)
		}

		return fmt.Errorf("unknown keys found during decoding:\n%s", string(data))
	}

	return nil
}
