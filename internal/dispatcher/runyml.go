package dispatcher

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/meshcloud/building-block-runner/internal/crypto"
)

// EncodeRunYaml goes through JSON, as the run holds raw JSON values.
func EncodeRunYaml(run Run) ([]byte, error) {
	encoded, err := json.Marshal(run)
	if err != nil {
		return nil, err
	}
	return jsonToYaml(encoded)
}

func DecodeRunYaml(data []byte) (run Run, err error) {
	encoded, err := yamlToJson(data)
	if err != nil {
		return
	}
	err = json.Unmarshal(encoded, &run)
	return
}

func ReadRunYaml(path string) (Run, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Run{}, err
	}
	run, err := DecodeRunYaml(data)
	if err != nil {
		return Run{}, fmt.Errorf("run file %s: %w", path, err)
	}
	return run, nil
}

func RedactedRunYaml(run Run) string {
	run, err := crypto.RedactRun(run)
	if err != nil {
		return fmt.Sprintf("<cannot redact the run: %v>", err)
	}
	encoded, err := EncodeRunYaml(run)
	if err != nil {
		return fmt.Sprintf("<cannot encode the run: %v>", err)
	}
	return string(encoded)
}

func jsonToYaml(value jsontext.Value) ([]byte, error) {
	tree, err := yamlTree(jsontext.NewDecoder(bytes.NewReader(value)))
	if err != nil {
		return nil, err
	}
	return yaml.MarshalWithOptions(tree, yaml.UseLiteralStyleIfMultiline(true))
}

// yamlNumber keeps every digit of a JSON number, which a float64 would round.
type yamlNumber string

func (n yamlNumber) MarshalYAML() ([]byte, error) {
	return []byte(n), nil
}

func yamlTree(decoder *jsontext.Decoder) (any, error) {
	token, err := decoder.ReadToken()
	if err != nil {
		return nil, err
	}
	switch token.Kind() {
	case '{':
		mapping := yaml.MapSlice{}
		for decoder.PeekKind() != '}' {
			key, err := decoder.ReadToken()
			if err != nil {
				return nil, err
			}
			item := yaml.MapItem{Key: key.String()}
			if item.Value, err = yamlTree(decoder); err != nil {
				return nil, err
			}
			mapping = append(mapping, item)
		}
		_, err = decoder.ReadToken()
		return mapping, err
	case '[':
		sequence := []any{}
		for decoder.PeekKind() != ']' {
			value, err := yamlTree(decoder)
			if err != nil {
				return nil, err
			}
			sequence = append(sequence, value)
		}
		_, err = decoder.ReadToken()
		return sequence, err
	case '"':
		return token.String(), nil
	case '0':
		return yamlNumber(token.String()), nil
	case 't', 'f':
		return token.Bool(), nil
	default:
		return nil, nil
	}
}

func yamlToJson(data []byte) (jsontext.Value, error) {
	file, err := parser.ParseBytes(data, 0)
	if err != nil {
		return nil, err
	}
	var encoded bytes.Buffer
	encoder := jsontext.NewEncoder(&encoded)
	if len(file.Docs) != 1 || file.Docs[0].Body == nil {
		err = encoder.WriteToken(jsontext.Null)
	} else {
		err = writeJson(encoder, file.Docs[0].Body)
	}
	return bytes.TrimSpace(encoded.Bytes()), err
}

func writeJson(encoder *jsontext.Encoder, node ast.Node) error {
	switch node := node.(type) {
	case *ast.MappingNode:
		return writeJsonObject(encoder, node.Values)
	case *ast.MappingValueNode:
		return writeJsonObject(encoder, []*ast.MappingValueNode{node})
	case *ast.SequenceNode:
		if err := encoder.WriteToken(jsontext.BeginArray); err != nil {
			return err
		}
		for _, element := range node.Values {
			if err := writeJson(encoder, element); err != nil {
				return err
			}
		}
		return encoder.WriteToken(jsontext.EndArray)
	case *ast.StringNode:
		return encoder.WriteToken(jsontext.String(node.Value))
	case *ast.LiteralNode:
		return encoder.WriteToken(jsontext.String(node.Value.Value))
	case *ast.IntegerNode:
		return writeJsonNumber(encoder, node.GetToken().Value, node.Value)
	case *ast.FloatNode:
		return writeJsonNumber(encoder, node.GetToken().Value, node.Value)
	case *ast.BoolNode:
		return encoder.WriteToken(jsontext.Bool(node.Value))
	case *ast.NullNode:
		return encoder.WriteToken(jsontext.Null)
	}
	return fmt.Errorf("line %d: %s has no JSON equivalent", node.GetToken().Position.Line, node.Type())
}

func writeJsonObject(encoder *jsontext.Encoder, entries []*ast.MappingValueNode) error {
	if err := encoder.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := encoder.WriteToken(jsontext.String(entry.Key.GetToken().Value)); err != nil {
			return err
		}
		if err := writeJson(encoder, entry.Value); err != nil {
			return err
		}
	}
	return encoder.WriteToken(jsontext.EndObject)
}

// writeJsonNumber falls back to the parsed value for a YAML number that is no JSON number, like 0x1f.
func writeJsonNumber(encoder *jsontext.Encoder, literal string, parsed any) error {
	if jsontext.Value(literal).IsValid() {
		return encoder.WriteValue(jsontext.Value(literal))
	}
	switch parsed := parsed.(type) {
	case float64:
		return encoder.WriteToken(jsontext.Float(parsed))
	case int64:
		return encoder.WriteToken(jsontext.Int(parsed))
	case uint64:
		return encoder.WriteToken(jsontext.Uint(parsed))
	}
	return errors.New("number " + literal + " has no JSON equivalent")
}
