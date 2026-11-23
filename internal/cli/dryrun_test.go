package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

// Every command takes --dry-run, so a script or an agent can add it without asking which ones do.
func TestEveryCommandTakesDryRun(t *testing.T) {
	root := (&env{}).rootCmd()
	var walk func(c *cobra.Command)
	n := 0
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			n++
			if sub.InheritedFlags().Lookup("dry-run") == nil && sub.Flags().Lookup("dry-run") == nil {
				t.Errorf("%s has no --dry-run", sub.CommandPath())
			}
			walk(sub)
		}
	}
	walk(root)
	if n < 30 {
		t.Errorf("only %d commands were checked", n)
	}
}
