package lanovod

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ygelfand/LANovo/internal/egress"
	"github.com/ygelfand/LANovo/internal/layout"
)

func newEgressCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "egress",
		Short: "Block everything off the local network, when network.internet is local",
		Long: "With the network.internet setting at \"local\", loads a firewall chain that lets the\n" +
			"display reach only the networks in " + layout.EgressConfigPath + " and rejects\n" +
			"the rest; otherwise removes it. init runs \"apply\" when netd starts, and lanovod\n" +
			"when the setting changes; run it after editing the file.",
	}
	c.AddCommand(
		&cobra.Command{
			Use:   "apply",
			Short: "Load or remove the block to match network.internet",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				wanted, err := wantedFromState()
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "%v; leaving the internet open\n", err)
				}
				conf, own, err := egress.Run(egress.Exec{}, layout.EgressConfigPath, wanted,
					func() { time.Sleep(time.Second) })
				if err != nil {
					return err
				}
				state := "off"
				if conf.Enabled {
					state = "on"
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "egress block %s, %d networks allowed, own %v\n",
					state, len(conf.Allow), own)
				return err
			},
		},
		&cobra.Command{
			Use:   "rules",
			Short: "Print the rules the block loads, without loading them",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				conf, own, err := egress.Inputs(layout.EgressConfigPath)
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "# iptables\n%s", egress.Rules(conf, egress.V4, own))
				_, err = fmt.Fprintf(out, "# ip6tables\n%s", egress.Rules(conf, egress.V6, own))
				return err
			},
		},
	)
	return c
}

// wantedFromState reads network.internet from the saved settings. A device with none saved yet
// is open.
func wantedFromState() (bool, error) {
	raw, err := os.ReadFile(layout.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("egress: %w", err)
	}
	return egress.Wanted(raw)
}
