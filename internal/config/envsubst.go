package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/token"
)

const noEnvsubstTag = "!no-envsubst"

// substituteEnv resolves the placeholders in all string values of the yaml, except for values tagged with noEnvsubstTag.
func substituteEnv(root ast.Node) error {
	s := &envSubstituter{}
	ast.Walk(s, root)
	return errors.Join(s.errs...)
}

type envSubstituter struct {
	errs []error
}

func (s *envSubstituter) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {
	case *ast.TagNode:
		if n.Start.Value != noEnvsubstTag {
			n.Value = s.resolve(n.Value, false)
		}
	case *ast.AnchorNode:
		n.Value = s.resolve(n.Value, true)
	case *ast.MappingValueNode:
		s.errs = append(s.errs, n.Replace(s.resolve(n.Value, true)))
	case *ast.SequenceNode:
		for i, value := range n.Values {
			s.errs = append(s.errs, n.Replace(i, s.resolve(value, true)))
		}
	default:
		return s
	}
	return nil
}

// resolve returns a new node for a value with placeholders, as goccy decodes a TextUnmarshaler from the tokens of a node.
// An untagged unquoted value gets the type of its resolved value, so that `${ENABLED:false}` decodes into a bool.
func (s *envSubstituter) resolve(node ast.Node, retype bool) ast.Node {
	var text string
	switch n := node.(type) {
	case *ast.StringNode:
		text = n.Value
	case *ast.LiteralNode:
		text = n.Value.Value
	default:
		ast.Walk(s, node)
		return node
	}
	if !strings.Contains(text, "${") {
		return node
	}
	value, err := expandPlaceholders(text)
	if err != nil {
		s.errs = append(s.errs, fmt.Errorf("line %d: %w", node.GetToken().Position.Line, err))
		return node
	}
	position := node.GetToken().Position
	if retype && node.GetToken().Type == token.StringType {
		if typed := typedScalar(value, position); typed != nil {
			return typed
		}
	}
	return ast.String(token.DoubleQuote(value, strconv.Quote(value), position))
}

func typedScalar(value string, position *token.Position) ast.Node {
	tk := token.New(value, value, position)
	switch tk.Type {
	case token.BoolType:
		return ast.Bool(tk)
	case token.IntegerType, token.BinaryIntegerType, token.OctetIntegerType, token.HexIntegerType:
		return ast.Integer(tk)
	case token.FloatType:
		return ast.Float(tk)
	case token.NullType:
		return ast.Null(tk)
	}
	return nil
}

// expandPlaceholders resolves ${NAME} and ${NAME:default} as Spring does.
// A default may contain placeholders, and \${ escapes a placeholder.
func expandPlaceholders(s string) (string, error) {
	var out strings.Builder
	for {
		start := strings.Index(s, "${")
		switch {
		case start < 0:
			out.WriteString(s)
			return out.String(), nil
		case start > 0 && s[start-1] == '\\':
			out.WriteString(s[:start-1] + "${")
			s = s[start+2:]
			continue
		}
		out.WriteString(s[:start])
		end := closingBrace(s, start+2)
		if end < 0 {
			return "", fmt.Errorf("placeholder %s has no closing brace", s[start:])
		}
		value, err := resolvePlaceholder(s[start+2 : end])
		if err != nil {
			return "", err
		}
		out.WriteString(value)
		s = s[end+1:]
	}
}

func closingBrace(s string, from int) int {
	depth := 0
	for i := from; i < len(s); i++ {
		switch {
		case strings.HasPrefix(s[i:], "${"):
			depth++
			i++
		case s[i] == '}' && depth == 0:
			return i
		case s[i] == '}':
			depth--
		}
	}
	return -1
}

func resolvePlaceholder(placeholder string) (string, error) {
	name, fallback, hasFallback := strings.Cut(placeholder, ":")
	if name == "" {
		return "", fmt.Errorf("placeholder ${%s} has no name", placeholder)
	}
	if value := lookupEnv(name); value != "" {
		return value, nil
	}
	if !hasFallback {
		return "", fmt.Errorf("environment variable %s of placeholder ${%s} is not set or empty", name, placeholder)
	}
	return expandPlaceholders(fallback)
}

var springRelaxedEnvName = strings.NewReplacer(".", "_", "-", "")

// lookupEnv also finds runner.api-url as RUNNER_APIURL, like Spring's relaxed binding does.
func lookupEnv(name string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return os.Getenv(springRelaxedEnvName.Replace(strings.ToUpper(name)))
}
