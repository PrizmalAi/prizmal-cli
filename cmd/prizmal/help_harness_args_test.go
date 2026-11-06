package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/PrizmalAi/prizmal-cli/internal/config"
	"github.com/PrizmalAi/prizmal-cli/internal/launcher/registry"
)

// jsonFieldName returns the name a struct field carries in the config file,
// without the ",omitempty" that may follow it on the tag.
func jsonFieldName(t *testing.T, typ reflect.Type, field string) string {
	t.Helper()
	f, ok := typ.FieldByName(field)
	if !ok {
		t.Fatalf("%s has no field %s", typ, field)
	}
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" || name == "-" {
		t.Fatalf("%s.%s has no json name", typ, field)
	}
	return name
}

// TestHelpDocumentsConfiguredHarnessArgs pins the help text's paragraph about
// per-harness launch arguments to the keys the loader reads. It is the only way
// to add a flag to every launch of one harness, and --help described
// ~/.prizmal/config.json for base_url and api_key alone, so the file read as a
// credential store with no argument surface at all. The section was documented
// in README.md and in each harness's doc under docs/, and reachable from
// neither --help nor the file's own contents, which on a machine that never
// saved a setting is absent.
//
// The names come from the struct tags and from the registry rather than from a
// copy of the example, so renaming the section, or adding a harness without its
// name in the paragraph, fails here instead of shipping a help text that names
// a key no build reads.
//
// The paragraph names the integration rather than the word an operator types,
// because withConfiguredArgs reads the config by the spec's canonical name. An
// alias is resolved before the lookup, so an entry stored under an alias would
// be ignored without a word, and no integration declares one today.
func TestHelpDocumentsConfiguredHarnessArgs(t *testing.T) {
	section := jsonFieldName(t, reflect.TypeOf(config.Config{}), "Harnesses")
	args := jsonFieldName(t, reflect.TypeOf(config.HarnessConfig{}), "Args")

	for _, name := range []string{section, args} {
		if !strings.Contains(harnessArgsHelp, `"`+name+`"`) {
			t.Errorf("--help's harness-arguments paragraph does not name %q\n"+
				"the config file reads it; a reader who edits the file by hand needs\n"+
				"the key spelled the way the loader reads it", name)
		}
	}
	if !strings.Contains(harnessArgsHelp, "~/.prizmal/config.json") {
		t.Error("--help's harness-arguments paragraph does not name the config file, " +
			"so it does not read as another thing that file holds")
	}
	for _, spec := range registry.ListAllIntegrationSpecs() {
		if !strings.Contains(harnessArgsHelp, spec.Name) {
			t.Errorf("--help's harness-arguments paragraph does not name the %q integration, "+
				"so an operator cannot tell what to key its args section by", spec.Name)
		}
	}
}
