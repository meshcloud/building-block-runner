package tfrun

import (
	"embed"
	"io/fs"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/meshcloud/meshfed-release/buildingblocks/tf-block-runner/util"
	"github.com/sebdah/goldie/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	//go:embed testdata/tf-variables
	testdataWorkingDirFs embed.FS
)

func TestParseVariableInputs(t *testing.T) {
	inputs, diags := ParseVariableInputs(util.Must(fs.Sub(testdataWorkingDirFs, "testdata/tf-variables")))
	require.Empty(t, diags)
	require.Equal(t, VariableInputs{
		"var1": VariableInput{Type: "any"},
		"var2": VariableInput{Type: "string"},
		"var3": VariableInput{Type: "any"},
		"var4": VariableInput{Type: "object({\n    some = string\n    flag = bool\n  })"},
		"var5": VariableInput{Type: "any"},
	}, inputs)
}

func TestVarsFile_AddVariable(t *testing.T) {
	assertWarnings := func(t *testing.T, diags hcl.Diagnostics, containsAllOf ...string) {
		t.Helper()
		require.NotEmpty(t, containsAllOf)
		var allDetails []string
		for _, diag := range diags {
			allDetails = append(allDetails, diag.Detail)
		}
		for _, contains := range containsAllOf {
			assert.Contains(t, strings.Join(allDetails, "\n"), contains)
		}
	}

	f := NewEmptyVarsFile()

	const (
		// testRawHcl includes some nasty leading/preceding whitespace
		testRawHcl = `

      { some-var = "bla"
      some_flag: true, nested = { "bla": "blub" } }

    `
		testRawJson = `[{ "some-var": "bla", "some_flag": true}, {"other-object": { "nested" : 1.1 }  }]`

		testRawYaml = `
# this is a comment
some-key: value1 # this as well
nested:
  bla: foo
`
	)

	t.Run("Clean", func(t *testing.T) {
		require.Empty(t, f.AddVariable("var1", true, AddVariableOptions{}))
		require.Empty(t, f.AddVariable("var2", "some-string", AddVariableOptions{}))

		require.Empty(t, f.AddRawVariable("var3", testRawHcl, AddVariableOptions{}))
		require.Empty(t, f.AddRawVariable("var4", testRawJson, AddVariableOptions{}))
		require.Empty(t, f.AddVariable("var5", 1.1, AddVariableOptions{}))
		require.Empty(t, f.AddVariable("var6", nil, AddVariableOptions{}))
		require.Empty(t, f.AddVariable("var7", []any{"item1", "item2"}, AddVariableOptions{}))

		require.Empty(t, f.AddRawVariable("var8", `"justword"`, AddVariableOptions{}))
	})

	t.Run("Clean, with JSON Encoding", func(t *testing.T) {
		opts := AddVariableOptions{EncodeAsJsonString: true}
		require.Empty(t, f.AddVariable("encoded_var1", true, opts))
		require.Empty(t, f.AddVariable("encoded_var2", "some-string", opts))

		require.Empty(t, f.AddRawVariable("encoded_var3", testRawHcl, opts))
		require.Empty(t, f.AddRawVariable("encoded_var4", testRawJson, opts))
		require.Empty(t, f.AddVariable("encoded_var5", 1.1, opts))
		require.Empty(t, f.AddVariable("encoded_var6", nil, opts))
		require.Empty(t, f.AddVariable("encoded_var7", []any{"item1", "item2"}, opts))
		require.Empty(t, f.AddRawVariable("encoded_var8", `"justword"`, opts))
	})

	t.Run("With warnings", func(t *testing.T) {
		assertWarnings(t, f.AddRawVariable("weird-var1", `justbareword`, AddVariableOptions{}),
			"Variables may not be used here.",
			`Cannot parse raw HCL 'justbareword' as value expression, will fallback to string variable`,
		)
		assertWarnings(t, f.AddRawVariable("weird-var2", `{{]]`, AddVariableOptions{}),
			"Expected the start of an expression, but found an invalid expression token.",
			"Cannot parse raw HCL '{{]]' as value expression, will fallback to string variable",
		)
		assertWarnings(t, f.AddRawVariable("weird-var3", "\n\t  \t", AddVariableOptions{}),
			"Expected the start of an expression, but found the end of the file.",
			"Cannot parse raw HCL '\n\t  \t' as value expression, will fallback to string variable",
		)
		assertWarnings(t, f.AddRawVariable("weird-var4", " ", AddVariableOptions{}),
			"Expected the start of an expression, but found the end of the file.",
			"Cannot parse raw HCL ' ' as value expression, will fallback to string variable",
		)

		assertWarnings(t, f.AddRawVariable("weird-var4", testRawYaml, AddVariableOptions{}),
			"An expression was successfully parsed, but extra characters were found after it.",
			"Cannot parse raw HCL '\n# this is a comment\nsome-key: value1 # this as well\nnested:\n  bla: foo\n' as value expression, will fallback to string variable",
		)
	})

	assert.ElementsMatch(t, f.VariableNames(), []string{
		"encoded_var1",
		"encoded_var2",
		"encoded_var3",
		"encoded_var4",
		"encoded_var5",
		"encoded_var6",
		"encoded_var7",
		"encoded_var8",
		"var1",
		"var2",
		"var3",
		"var4",
		"var5",
		"var6",
		"var7",
		"var8",
		"weird-var1",
		"weird-var2",
		"weird-var3",
		"weird-var4",
	})

	g := goldie.New(t, goldie.WithNameSuffix(".golden.tfvars"))
	g.Assert(t, "expected", f.Bytes())
}
