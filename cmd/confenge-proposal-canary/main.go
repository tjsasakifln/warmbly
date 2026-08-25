package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/warmbly/warmbly/internal/app/confenge/proposal"
)

func main() {
	proposalOnly := flag.Bool("proposal", false, "print the accepted proposal instead of the delivery handoff")
	flag.Parse()
	result, err := proposal.RunSyntheticCanary(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	value := any(result.Handoff)
	if *proposalOnly {
		value = result.Proposal
	}
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
