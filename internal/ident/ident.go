// Package ident generates mnemonic worker identifiers. A worker's id is its
// only identity and vantage point (DESIGN 3.2), so it must be human-readable,
// URL-safe, and match ^[A-Za-z0-9-]+$. The worker self-assigns one at startup;
// the coordinator falls back to generating one when a worker registers without.
package ident

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// adjectives and animals are kept short and lowercase ASCII so every generated
// id satisfies ^[A-Za-z0-9-]+$.
var adjectives = []string{
	"brave", "calm", "clever", "eager", "fancy", "gentle", "happy", "jolly",
	"keen", "lively", "merry", "noble", "proud", "quick", "rapid", "shiny",
	"swift", "tidy", "vivid", "witty", "bold", "crisp", "deep", "fair",
}

var animals = []string{
	"otter", "falcon", "lynx", "panda", "tiger", "heron", "marten", "raven",
	"badger", "ferret", "gecko", "ibex", "jackal", "koala", "lemur", "moth",
	"newt", "owl", "puma", "quail", "robin", "stoat", "tapir", "vole",
}

// Mnemonic returns a fresh worker id like "swift-otter-4821".
func Mnemonic() string {
	return fmt.Sprintf("%s-%s-%04d", pick(adjectives), pick(animals), randN(10000))
}

func pick(xs []string) string { return xs[randN(len(xs))] }

// randN returns a uniform integer in [0, n) using crypto/rand. On the
// (practically impossible) failure of the system RNG it falls back to 0, which
// only biases the mnemonic, never breaks it.
func randN(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}
