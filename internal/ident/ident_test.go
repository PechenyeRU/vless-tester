package ident

import (
	"regexp"
	"testing"
)

func TestMnemonicMatchesPattern(t *testing.T) {
	re := regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	for i := 0; i < 100; i++ {
		id := Mnemonic()
		if id == "" {
			t.Fatal("empty mnemonic")
		}
		if !re.MatchString(id) {
			t.Fatalf("mnemonic %q does not match ^[A-Za-z0-9-]+$", id)
		}
	}
}

func TestMnemonicVaries(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		seen[Mnemonic()] = true
	}
	// With ~24*24*10000 possibilities, 50 draws colliding into <5 distinct
	// values would mean the generator is effectively constant.
	if len(seen) < 5 {
		t.Fatalf("mnemonic not varying: only %d distinct of 50", len(seen))
	}
}
