package main

import (
	"os"

	"github.com/spf13/cobra"
	_"github.com/teddy/sign-in-on/model/wechat"
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringP("config", "c", "config.json", "Configuration file to use.")
	rootCmd.PersistentFlags().Bool("disableconfigwatch", false, "When set config.json will not be loaded from disk when the file is changed.")
	rootCmd.AddCommand(serverCmd)
}

var rootCmd = &cobra.Command{
	Use:   "sign-on-in",
	Short: "capp",
	Long:  `sign-in-on-in`,
	RunE:  runServerCmd,
}
