// Package builtins implements the `neuron builtins` command, which reports the
// capability runtimes the runtime provides without installation.
package builtins

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/neuron-runtime/neuron/application/internal/cli/command"
	"github.com/neuron-runtime/neuron/shared/types/core"
	"github.com/spf13/cobra"
)

// New returns the `neuron builtins` command.
//
// The command reports a contract that is compiled into the runtime, so it
// needs neither a running runtime nor a project. It is the place an author
// looks up what a built-in accepts and what it guarantees.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   command.Builtins,
		Short: "Report the built-in capability runtimes",
		Long: `Report the capability runtimes N.O.R.E. provides without installation.

A built-in capability runtime is part of the runtime: it is always available, it is
never installed from a registry, and it is the capability runtime an assembly uses
when it names none.

The command reports the contract of every built-in, or the contract of one built-in
when it is named. Use --verbose for the full descriptions.

The command needs neither a running runtime nor a project.`,
		Args: cobra.MaximumNArgs(1),
		RunE: run,
	}
	// The command declares the flag it reads, so it reports the contract the
	// same way whether it runs on its own or under the root command. The root
	// command has a verbose flag of its own with a different meaning, and this
	// command shows no runtime logs.
	cmd.Flags().BoolP("verbose", "v", false, "Include the full descriptions")
	return cmd
}

// run reports the built-ins, or the one that was named.
func run(cmd *cobra.Command, args []string) error {
	verbose, _ := cmd.Flags().GetBool("verbose")
	out := cmd.OutOrStdout()

	if len(args) == 1 {
		return describe(out, args[0], verbose)
	}
	return list(out, verbose)
}

// list reports every built-in.
func list(out io.Writer, verbose bool) error {
	descriptors := core.BuiltinDescriptors()
	// The listing is sorted by name so it reads the same way as the list of
	// available built-ins in an error message.
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Name < descriptors[j].Name })

	writer := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, descriptor := range descriptors {
		if _, err := fmt.Fprintf(writer, "%s\t%s\n", descriptor.Type(), descriptor.Summary); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}

	if verbose {
		for _, descriptor := range descriptors {
			if _, err := fmt.Fprintf(out, "\n%s\n  %s\n", descriptor.Type(), descriptor.Description); err != nil {
				return err
			}
		}
	}
	return nil
}

// describe reports one built-in.
func describe(out io.Writer, name string, verbose bool) error {
	descriptor, ok := core.LookupBuiltin(core.CapabilityRuntimeType(canonical(name)))
	if !ok {
		return unknown(name)
	}

	if _, err := fmt.Fprintf(out, "%s\n  %s\n", descriptor.Type(), descriptor.Summary); err != nil {
		return err
	}

	if verbose {
		if _, err := fmt.Fprintf(out, "\n%s\n", descriptor.Description); err != nil {
			return err
		}
	}

	if err := writeParameters(out, "Parameters", descriptor.Parameters, verbose); err != nil {
		return err
	}
	if err := writeParameters(out, "Results", descriptor.Results, verbose); err != nil {
		return err
	}

	if len(descriptor.Constraints) > 0 {
		if _, err := fmt.Fprintf(out, "\nConstraints:\n"); err != nil {
			return err
		}
		for _, constraint := range descriptor.Constraints {
			if _, err := fmt.Fprintf(out, "  - %s\n", constraint); err != nil {
				return err
			}
		}
	}

	return nil
}

// writeParameters writes the parameters or the results of a built-in.
func writeParameters(out io.Writer, title string, parameters []core.BuiltinParameter, verbose bool) error {
	if len(parameters) == 0 {
		return nil
	}

	if _, err := fmt.Fprintf(out, "\n%s:\n", title); err != nil {
		return err
	}

	for _, parameter := range parameters {
		if _, err := fmt.Fprintf(out, "  %s (%s%s)\n", parameter.Name, parameter.Type, qualification(parameter)); err != nil {
			return err
		}
		if verbose && parameter.Description != "" {
			if _, err := fmt.Fprintf(out, "    %s\n", parameter.Description); err != nil {
				return err
			}
		}
	}
	return nil
}

// qualification states how a parameter is satisfied: it is required, it has
// a default, or it is optional.
func qualification(parameter core.BuiltinParameter) string {
	switch {
	case parameter.Required:
		return ", required"
	case parameter.Default != "":
		return fmt.Sprintf(", default %q", parameter.Default)
	default:
		return ""
	}
}

// canonical turns a name into the canonical form of a core capability runtime,
// so that a name that names a removed built-in is reported as removed rather
// than as unknown.
func canonical(name string) string {
	trimmed := strings.TrimSpace(name)
	if strings.Contains(trimmed, ":") {
		return trimmed
	}
	return string(core.CoreName(trimmed))
}

// unknown reports a name that names no built-in.
func unknown(name string) error {
	canonicalName := canonical(name)
	if err := core.UnknownBuiltinError(core.CapabilityRuntimeType(canonicalName)); err != nil {
		return err
	}
	return fmt.Errorf("unknown built-in capability runtime %q; the built-in capability runtimes are %s",
		name, strings.Join(core.BuiltinTypeNames(), ", "))
}
