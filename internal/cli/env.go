package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// envPrefix namespaces the variables so dross cannot be configured by accident
// from something another tool exported.
const envPrefix = "DROSS_"

// envKey is the variable a flag reads: --auth-client-id ← DROSS_AUTH_CLIENT_ID.
func envKey(flag string) string {
	return envPrefix + strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
}

// fromEnvAnnotation marks a flag whose value came from the environment, so a
// command that reports where a setting came from can tell the two apart.
const fromEnvAnnotation = "dross.sh/from-env"

// bindEnv lets every flag be set from the environment.
//
// Anything worth passing on the command line is worth setting once for a
// machine — the server it links to, the workspace it runs in, the model it
// prefers — and wiring that per flag meant the ones nobody had got round to
// silently ignored their variable. Deriving the name from the flag makes every
// flag, including ones added later, settable without further thought.
//
// A flag given on the command line wins: an explicit argument is a deliberate
// override of the environment it is being run in.
func bindEnv(cmd *cobra.Command) error {
	var failed error
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if failed != nil || f.Changed {
			return
		}
		v, ok := os.LookupEnv(envKey(f.Name))
		if !ok || v == "" {
			return
		}
		if err := f.Value.Set(v); err != nil {
			failed = fmt.Errorf("%s=%q: %w", envKey(f.Name), v, err)
			return
		}
		// Changed, because the layers below read it to decide whether a setting
		// was specified at all: an unmarked value would lose to the config file
		// it is supposed to override.
		f.Changed = true
		if f.Annotations == nil {
			f.Annotations = map[string][]string{}
		}
		f.Annotations[fromEnvAnnotation] = []string{envKey(f.Name)}
	})
	return failed
}

// envSource names the variable a flag was set from, or "" if it was not.
func envSource(cmd *cobra.Command, flag string) string {
	f := cmd.Flags().Lookup(flag)
	if f == nil {
		return ""
	}
	if v := f.Annotations[fromEnvAnnotation]; len(v) > 0 {
		return "$" + v[0]
	}
	return ""
}
