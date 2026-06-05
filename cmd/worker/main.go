// Command worker is a probe: it pulls jobs from the coordinator, runs the test
// battery through a local sing-box instance, and reports results. See DESIGN.md.
package main

import "fmt"

func main() {
	// Wiring is added in T1.2 (pull-based remote worker).
	fmt.Println("vless-tester worker: not implemented yet")
}
