package schema

import "encoding/json"

// JSONSchema emits the manifest's JSON Schema (draft 2020-12), so a Python
// consumer (ach-agent) can validate a manifest it authored without
// reimplementing Decode's rules. It is a hand-written literal, not generated
// by reflecting over Profile: a generator would drift the moment a Go field
// name diverged from its YAML key, silently. encoding/json sorts a map's keys
// when marshalling, which is what makes the output byte-stable across runs
// without this file tracking key order itself.
//
// It covers structural shape and the "exactly one of" pairs (§13/§14/§36),
// plus the one structural rule that names a literal key (Authorization is
// never a key of model.headers, §9). It does NOT encode business rules that
// span more than one property's presence against a THIRD property's value —
// an enabled resource needing exactly one of ref/source is one (§22), and a
// transport header named Authorization needing value_from rather than a
// literal value is another (§14) — because JSON Schema's conditional
// vocabulary (if/then over sibling keys) would make this file a second
// implementation of Decode rather than a companion to it. Those two rules
// still run, in Decode, on every manifest ap itself composes.
func JSONSchema() []byte {
	valueFrom := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"secret":   map[string]any{"type": "string"},
			"variable": map[string]any{"type": "string"},
		},
		"oneOf": []any{
			map[string]any{"required": []any{"secret"}},
			map[string]any{"required": []any{"variable"}},
		},
	}

	headerValue := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"value":      map[string]any{"type": "string"},
			"value_from": map[string]any{"$ref": "#/$defs/valueFrom"},
			"prefix":     map[string]any{"type": "string"},
		},
		"oneOf": []any{
			map[string]any{"required": []any{"value"}},
			map[string]any{"required": []any{"value_from"}},
		},
	}

	source := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"git":   map[string]any{"$ref": "#/$defs/gitSource"},
			"local": map[string]any{"$ref": "#/$defs/localSource"},
		},
		"oneOf": []any{
			map[string]any{"required": []any{"git"}},
			map[string]any{"required": []any{"local"}},
		},
	}

	gitSource := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"url"},
		"properties": map[string]any{
			"url":     map[string]any{"type": "string"},
			"ref":     map[string]any{"type": "string"},
			"subpath": map[string]any{"type": "string"},
			"auth":    map[string]any{"$ref": "#/$defs/valueFrom"},
		},
	}

	localSource := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"path"},
		"properties": map[string]any{
			"path":    map[string]any{"type": "string"},
			"subpath": map[string]any{"type": "string"},
		},
	}

	resource := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"enabled": map[string]any{"type": "boolean"},
			"ref":     map[string]any{"type": "string"},
			"source":  map[string]any{"$ref": "#/$defs/source"},
		},
	}

	transport := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"type"},
		"properties": map[string]any{
			"type":    map[string]any{"enum": []any{"http", "stdio"}},
			"url":     map[string]any{"type": "string"},
			"headers": map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/headerValue"}},
			"command": map[string]any{"type": "string"},
			"args":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}

	mcp := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"transport"},
		"properties": map[string]any{
			"enabled":   map[string]any{"type": "boolean"},
			"transport": map[string]any{"$ref": "#/$defs/transport"},
		},
	}

	artifact := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"enabled":     map[string]any{"type": "boolean"},
			"source":      map[string]any{"$ref": "#/$defs/source"},
			"destination": map[string]any{"type": "string"},
		},
	}

	marketplace := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"type", "source"},
		"properties": map[string]any{
			"enabled": map[string]any{"type": "boolean"},
			"type":    map[string]any{"enum": []any{"plugins", "skills"}},
			"source":  map[string]any{"$ref": "#/$defs/source"},
		},
	}

	auth := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"type", "value_from"},
		"properties": map[string]any{
			"type":       map[string]any{"const": "bearer"},
			"value_from": map[string]any{"$ref": "#/$defs/valueFrom"},
		},
	}

	model := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"type":     map[string]any{"type": "string"},
			"base_url": map[string]any{"type": "string"},
			"model":    map[string]any{"type": "string"},
			"auth":     map[string]any{"$ref": "#/$defs/auth"},
			// §9: Authorization is never a key here — endpoint auth has
			// exactly one representation, model.auth.
			"headers": map[string]any{
				"type":                 "object",
				"propertyNames":        map[string]any{"not": map[string]any{"const": "Authorization"}},
				"additionalProperties": map[string]any{"$ref": "#/$defs/headerValue"},
			},
			"parameters": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		},
	}

	prompt := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"mode":    map[string]any{"enum": []any{"append", "replace"}},
			"content": map[string]any{"type": "string"},
			"source":  map[string]any{"$ref": "#/$defs/source"},
		},
		"oneOf": []any{
			map[string]any{"required": []any{"content"}},
			map[string]any{"required": []any{"source"}},
		},
	}

	binding := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"env":  map[string]any{"type": "string"},
			"file": map[string]any{"type": "string"},
		},
		"oneOf": []any{
			map[string]any{"required": []any{"env"}},
			map[string]any{"required": []any{"file"}},
		},
	}

	inputs := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"variables": map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/binding"}},
			"secrets":   map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/binding"}},
		},
	}

	runtime := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"skills":       map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/resource"}},
			"mcps":         map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/mcp"}},
			"artifacts":    map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/artifact"}},
			"marketplaces": map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/marketplace"}},
			"model":        map[string]any{"$ref": "#/$defs/model"},
			"prompt":       map[string]any{"$ref": "#/$defs/prompt"},
			"inputs":       map[string]any{"$ref": "#/$defs/inputs"},
			// §24: plugins is adapter-owned and unvalidated at this layer.
			"plugins":     map[string]any{"type": "object"},
			"environment": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"variants":    map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
		},
	}

	schema := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "https://ackstorm.github.io/agent-profile/schema/manifest-v1.json",
		"title":   "agent-profile declarative manifest v1",
		"type":    "object",
		// §36's Profile section: version and name are required; a base
		// profile omits targets, so targets is not.
		"required":             []any{"version", "name"},
		"additionalProperties": false,
		"properties": map[string]any{
			"version": map[string]any{"const": "1"},
			"name":    map[string]any{"type": "string", "minLength": 1},
			// extends is consumed before Decode ever sees the tree (§5.2);
			// it is still a valid key of an AUTHORED manifest, which is what
			// this schema describes.
			"extends":      map[string]any{"type": "string"},
			"targets":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"model":        map[string]any{"$ref": "#/$defs/model"},
			"prompt":       map[string]any{"$ref": "#/$defs/prompt"},
			"inputs":       map[string]any{"$ref": "#/$defs/inputs"},
			"marketplaces": map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/marketplace"}},
			"skills":       map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/resource"}},
			"mcps":         map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/mcp"}},
			"artifacts":    map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/artifact"}},
			"runtimes":     map[string]any{"type": "object", "additionalProperties": map[string]any{"$ref": "#/$defs/runtime"}},
		},
		"$defs": map[string]any{
			"valueFrom":   valueFrom,
			"headerValue": headerValue,
			"source":      source,
			"gitSource":   gitSource,
			"localSource": localSource,
			"resource":    resource,
			"transport":   transport,
			"mcp":         mcp,
			"artifact":    artifact,
			"marketplace": marketplace,
			"auth":        auth,
			"model":       model,
			"prompt":      prompt,
			"binding":     binding,
			"inputs":      inputs,
			"runtime":     runtime,
		},
	}

	b, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		// Every value above is a literal built from maps, slices, strings and
		// bools — nothing marshal can refuse. A panic here means the literal
		// itself is broken, which a test catches long before this ships.
		panic(err)
	}
	return b
}
