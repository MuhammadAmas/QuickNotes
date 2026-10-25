package tools

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// inputSchema infers the JSON Schema for the input struct T (exactly as mcp.AddTool would) and
// lets customize add rules struct tags can't express: allowed values, ranges, patterns.
// The SDK validates every call against it, and AI clients read it to learn what is allowed.
func inputSchema[T any](customize func(props map[string]*jsonschema.Schema)) *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("inferring input schema: %v", err)) // a programming mistake: fail at startup
	}
	customize(schema.Properties)
	return schema
}

func enum(values ...string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

const datePattern = `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`

func dateProp(p *jsonschema.Schema) {
	p.Pattern = datePattern
	p.Format = "date"
}

func rangeProp(p *jsonschema.Schema, min, max, def int) {
	p.Minimum = jsonschema.Ptr(float64(min))
	p.Maximum = jsonschema.Ptr(float64(max))
	if def > 0 {
		p.Default = []byte(fmt.Sprint(def))
	}
}

func lengthProp(p *jsonschema.Schema, min, max int) {
	p.MinLength = jsonschema.Ptr(min)
	p.MaxLength = jsonschema.Ptr(max)
}

func idProp(p *jsonschema.Schema) {
	p.Pattern = idPattern.String()
}
