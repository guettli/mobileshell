package main

import (
	"fmt"
	"os"
	"strings"

	"mobileshell/internal/auth"
	"mobileshell/internal/nohup"
	"mobileshell/internal/server"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	stateDir  string
	port      string
	allowRoot bool
	debugHTML bool

	inputUnixDomainSocket string
	workingDirectory      string
)

var rootCmd = &cobra.Command{
	Use:   "mobileshell",
	Short: "MobileShell - Remote command execution server",
	Long:  `MobileShell is a web-based server for executing commands remotely with output streaming.`,
}

// checkRootUser returns an error if running as root and allowRoot is false
func checkRootUser(allowRoot bool) error {
	if os.Geteuid() == 0 && !allowRoot {
		return fmt.Errorf("running as root is not allowed for security reasons. Use --allow-root to override")
	}
	return nil
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the MobileShell server",
	Long:  `Start the MobileShell server to accept remote command execution requests.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkRootUser(allowRoot); err != nil {
			return err
		}
		return server.Run(stateDir, port, debugHTML)
	},
}

var fromStdin bool

var addPasswordCmd = &cobra.Command{
	Use:           "add-password",
	Short:         "Add a password for authentication",
	Long:          fmt.Sprintf("Read a password from stdin and add it to the hashed-passwords directory. The password must be at least %d characters long.", auth.MinPasswordLength),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkRootUser(allowRoot); err != nil {
			return err
		}

		// Get state directory, don't create it if it doesn't exist
		dir, err := server.GetStateDir(stateDir, false)
		if err != nil {
			return err
		}

		var password string
		if fromStdin {
			// Read password from stdin without prompting
			passwordBytes, err := os.ReadFile("/dev/stdin")
			if err != nil {
				return fmt.Errorf("failed to read password from stdin: %w", err)
			}
			password = strings.TrimSpace(string(passwordBytes))
		} else {
			// Read password from stdin without echoing
			fmt.Fprintf(os.Stderr, "Enter password (min %d characters, hint: openssl rand -base64 32): ", auth.MinPasswordLength)
			passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr) // Print newline after password input
			if err != nil {
				return fmt.Errorf("failed to read password: %w", err)
			}
			password = strings.TrimSpace(string(passwordBytes))
		}

		// Add the password
		if err := auth.AddPassword(dir, password); err != nil {
			return fmt.Errorf("add password failed: %w", err)
		}

		fmt.Fprintln(os.Stderr, "Password added successfully!")
		return nil
	},
}

var nohupCmd = &cobra.Command{
	Use:   "nohup cmd [args...]",
	Short: "Execute a process in nohup mode (internal use)",
	Long: `Execute a process in nohup mode.

This command is used internally by the MobileShell server to run processes
in detached mode (nohup). It handles process execution, output capture,
and maintains process state in the workspace directory.

This way the server can be restarted, and the new server process can re-connect to the running
nohup processes.

Arguments:
  cmd: The command which gets executed.


The output will be written to the directory containing 'cmd'.

This command should not be called directly by users. It is automatically
invoked by the server when executing processes in nohup mode.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) < 1 {
			return fmt.Errorf("not enough arguments")
		}
		return nohup.Run(args, inputUnixDomainSocket, workingDirectory)
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	runCmd.Flags().StringVarP(&stateDir, "state-dir", "s", "", "State directory for storing data (default: $STATE_DIRECTORY or .mobileshell)")
	runCmd.Flags().StringVarP(&port, "port", "p", "22123", "Port to listen on")
	runCmd.Flags().BoolVar(&allowRoot, "allow-root", false, "Allow running as root user (not recommended for security reasons)")
	runCmd.Flags().BoolVar(&debugHTML, "debug-html", false, "Validate HTML responses and return 500 on invalid HTML (for development)")

	addPasswordCmd.Flags().StringVarP(&stateDir, "state-dir", "s", "", "State directory for storing data (default: $STATE_DIRECTORY or .mobileshell)")
	addPasswordCmd.Flags().BoolVar(&fromStdin, "from-stdin", false, "Read password from stdin without prompting (for scripts)")
	addPasswordCmd.Flags().BoolVar(&allowRoot, "allow-root", false, "Allow running as root user (not recommended for security reasons)")

	nohupCmd.Flags().StringVar(&inputUnixDomainSocket, "input-unix-domain-socket", "", "Read input (like stdin and signals) from unix domain socket.")
	nohupCmd.Flags().StringVar(&workingDirectory, "working-directory", "", "Working directory for the command")

	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(addPasswordCmd)
	rootCmd.AddCommand(nohupCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
