package protocols_test

import (
	"os"
	"testing"

	"github.com/promisegrid/gridspec/internal/gridwire"
)

func TestFrozenProtocolCIDs(t *testing.T) {
	expected := map[string]string{
		"artifact-v1.md":                   "bafkreiasth6ndvmh3kfwiovo443uegjtti7tqfvxbdjzh4lgy5vqk3ltjy",
		"authored-transaction-event-v1.md": "bafkreic7upqan2eqeclsuarh3yumxs55kkv5yrdy3eduxgase75ezxl7c4",
		"conformance-evidence-v1.md":       "bafkreieoo4wajvap53gtqgfalfpag2zbszxrh77rxbsnesexq33oz4gowu",
		"entity-v1.md":                     "bafkreiex4wu4lzokpmgcbgaepcs4wyxrk3u2qwg3v522otvqn6w55vehja",
		"state-v1.md":                      "bafkreihmzn253eula4alvnya6tnt4o3s3aqzdjgjvn7qhess57u5sul2vq",
		"transaction-event-v1.md":          "bafkreibr7qlhtebidocxgsrktev34r2i3yw2ug656a7gtg2upqfpmde2tm",
		"transaction-language-v1.md":       "bafkreiag4as5cd5lmd2zjv3vocbixczrbrrzqwa5uxbg6w47w4nhqtdriq",
	}
	for name, want := range expected {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		value, err := gridwire.RawCID(data)
		if err != nil {
			t.Fatal(err)
		}
		if value.String() != want {
			t.Fatalf("%s pCID = %s, want %s", name, value, want)
		}
	}
}
