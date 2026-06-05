// Command coordinator is the control plane: it ingests proxy sources, owns the
// job queue and scheduler, and publishes the working list. See DESIGN.md.
package main

import "fmt"

func main() {
	// Wiring is added in later tasks (scheduler: T0.9, REST API: T1.1).
	fmt.Println("vless-tester coordinator: not implemented yet")
}
