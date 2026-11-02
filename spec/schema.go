// Package spec carries the JSON Schemas, so code checks records against the
// same files the specification publishes.
package spec

import (
	"bytes"
	"embed"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Base is the address every schema is published under.
const Base = "https://schema.trailpad.ai/memory/0.1/"

//go:embed schema/*.json
var files embed.FS

var (
	once     sync.Once
	compiled map[string]*jsonschema.Schema
	failed   error
)

func load() {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	names, err := files.ReadDir("schema")
	if err != nil {
		failed = err
		return
	}
	for _, f := range names {
		raw, err := files.ReadFile(path.Join("schema", f.Name()))
		if err != nil {
			failed = err
			return
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			failed = fmt.Errorf("%s: %w", f.Name(), err)
			return
		}
		if err := c.AddResource(Base+f.Name(), doc); err != nil {
			failed = err
			return
		}
	}
	compiled = map[string]*jsonschema.Schema{}
	for _, f := range names {
		s, err := c.Compile(Base + f.Name())
		if err != nil {
			failed = fmt.Errorf("%s: %w", f.Name(), err)
			return
		}
		compiled[strings.TrimSuffix(f.Name(), ".json")] = s
	}
}

func Schema(kind string) (*jsonschema.Schema, error) {
	once.Do(load)
	if failed != nil {
		return nil, failed
	}
	s, ok := compiled[kind]
	if !ok {
		return nil, fmt.Errorf("no schema for %q", kind)
	}
	return s, nil
}

// Validate checks raw JSON against the schema for one kind.
func Validate(kind string, raw []byte) error {
	s, err := Schema(kind)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return s.Validate(doc)
}
