package txnlang

import "testing"

func TestParseProgram(t *testing.T) {
	program, err := Parse([]byte(`gridspec 1
module "example.test"
profile "build"
put graph algorithm.main {
  kind = "algorithm"
  directed = true
  labels = ["one", "two"]
  attrs = { specdot_version = "1"; specdot_kind = "algorithm" }
}
assert exists graph algorithm.main
assert equals graph algorithm.main kind "algorithm"
`))
	if err != nil {
		t.Fatal(err)
	}
	if program.Module == nil || *program.Module != "example.test" || len(program.Ops) != 3 {
		t.Fatalf("unexpected program: %#v", program)
	}
}

func TestParseRejectsUnboundedOrAmbiguousSyntax(t *testing.T) {
	for _, source := range []string{
		"gridspec 1\nwhile true {}\n",
		"gridspec 1\nput graph g { a = 1; a = 2 }\n",
		"gridspec 1\r\n",
		"gridspec 1\nput graph g { a = unquoted }\n",
	} {
		if _, err := Parse([]byte(source)); err == nil {
			t.Fatalf("expected failure for %q", source)
		}
	}
}
