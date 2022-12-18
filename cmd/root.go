package cmd

import (
	"net"
	"os"
	"time"

	"code.cotugno.family/kevin/dlnaprox/internal"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var (
	opts  internal.Options
	debug bool
	trace bool
)

var rootCmd = &cobra.Command{
	Use:   "dnlaprox",
	Short: "Proxy multicast packets to another nextwork",
	Long: `Proxy multicast packets to another nextwork

dnlaprox  Copyright (C) 2022  Kevin Cotugno
This program comes with ABSOLUTELY NO WARRANTY.
This is free software, and you are welcome to redistribute it
under certain conditions.`,
	Run: func(cmd *cobra.Command, args []string) {
		var level zerolog.Level
		if trace {
			level = zerolog.TraceLevel
		} else if debug {
			level = zerolog.DebugLevel
		} else {
			level = zerolog.InfoLevel
		}

		log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339Nano}).
			With().
			Timestamp().
			Logger().
			Level(level)

		if err := internal.Start(opts); err != nil {
			log.Err(err).Send()
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().IPVar(&opts.OuterIP, "outer-ip", net.IP{}, "Specific IP address to bind to for the outer network")
	rootCmd.Flags().IPNetVarP(&opts.InnerNetwork, "inner-network", "i", net.IPNet{}, "IP address with network mask to denote the inner network")
	rootCmd.Flags().IPNetVarP(&opts.OuterNetwork, "outer-network", "o", net.IPNet{}, "IP address with network mask to denote the outer network")
	rootCmd.Flags().BoolVarP(&opts.Rewrite, "rewrite", "r", false, "Rewrite outgoing packet payloads with the outer network IP address in place of internal network IP")
	rootCmd.Flags().StringVar(&opts.Replacement, "replacement", "", "Explicit replacement in place of the outer network IP")
	rootCmd.Flags().StringVar(&opts.MatchPrefix, "prefix", "", "Also match this prefix to a matched IP for rewriting")
	rootCmd.Flags().StringVar(&opts.MatchPostfix, "postfix", "", "Also match this postfix to a matched IP for rewriting")
	rootCmd.Flags().BoolVar(&debug, "debug", false, "Print debug log events")
	rootCmd.Flags().BoolVar(&trace, "trace", false, "Print trace log events")
	rootCmd.MarkFlagRequired("inner-network")
	rootCmd.MarkFlagRequired("outer-network")
}
