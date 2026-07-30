package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dougkirkley/dross/internal/session"
	"github.com/dougkirkley/dross/internal/storage"
)

func newBriefCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "brief [name]",
		Short: "Print a saved handoff brief, or list sessions",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sp, err := mustStorePath()
			if err != nil {
				return err
			}
			store := storage.NewSessionStore(sp)
			out := cmd.OutOrStdout()

			if len(args) == 0 {
				names, err := session.List(store)
				if err != nil {
					return fmt.Errorf("list: %w", err)
				}
				if len(names) == 0 {
					fmt.Fprintln(out, "no saved sessions")
					return nil
				}
				for _, n := range names {
					fmt.Fprintln(out, n)
				}
				return nil
			}

			b, ok, err := session.Load(store, args[0])
			if err != nil {
				return fmt.Errorf("load: %w", err)
			}
			if !ok {
				return fmt.Errorf("no brief for session %q", args[0])
			}
			fmt.Fprintf(out, "# session %s (updated %s)\n\n%s\n", b.Name, b.Updated.Format("2006-01-02 15:04"), b.Content)
			return nil
		},
	}
}
