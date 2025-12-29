package main

import (
	"fmt"
	"os"
	"strings"

	"mobileshell/internal/auth"
	"mobileshell/internal/server"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	stateDir string
	port     string
)

var rootCmd = &cobra.Command{
	Use:   "mobileshell",
	Short: "MobileShell - Remote command execution server",
	Long:  `MobileShell is a web-based server for executing commands remotely with output streaming.`,
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the MobileShell server",
	Long:  `Start the MobileShell server to accept remote command execution requests.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return server.Run(stateDir, port)
	},
}

var addPasswordCmd = &cobra.Command{
	Use:           "add-password",
	Short:         "Add a password for authentication",
	Long:          fmt.Sprintf("Read a password from stdin and add it to the hashed-passwords directory. The password must be at least %d characters long.", auth.MinPasswordLength),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Get state directory, creating it if it doesn't exist
		dir, err := server.GetStateDir(stateDir, true)
		if err != nil {
			return err
		}

		// Read password from stdin without echoing
		fmt.Fprintf(os.Stderr, "Enter password (min %d characters, hint: openssl rand -base64 32): ", auth.MinPasswordLength)
		passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr) // Print newline after password input
		if err != nil {
			return fmt.Errorf("failed to read password: %w", err)
		}

		// Convert bytes to string and trim whitespace
		password := strings.TrimSpace(string(passwordBytes))

		// Add the password
		if err := auth.AddPassword(dir, password); err != nil {
			return fmt.Errorf("add password failed: %w", err)
		}

		fmt.Fprintln(os.Stderr, "Password added successfully!")
		return nil
	},
}

func init() {
	runCmd.Flags().StringVarP(&stateDir, "state-dir", "s", "", "State directory for storing data (default: $STATE_DIRECTORY or .mobileshell)")
	runCmd.Flags().StringVarP(&port, "port", "p", "22123", "Port to listen on")

	addPasswordCmd.Flags().StringVarP(&stateDir, "state-dir", "s", "", "State directory for storing data (default: $STATE_DIRECTORY or .mobileshell)")

	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(addPasswordCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
