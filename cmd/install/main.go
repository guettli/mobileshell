package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

//go:embed mobileshell.service
var systemdService string

func main() {
	if len(os.Args) != 3 {
		fmt.Println("Usage: go run ./cmd/install <hostname> <username>")
		fmt.Println("Example: go run ./cmd/install myserver.example.com myuser")
		os.Exit(1)
	}

	hostname := os.Args[1]
	username := os.Args[2]

	fmt.Printf("Installing MobileShell to %s as user %s\n", hostname, username)

	// Build the binary
	fmt.Println("Building mobileshell binary...")
	buildCmd := exec.Command("go", "build", "-o", "mobileshell", "./cmd/mobileshell")
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		log.Fatalf("Failed to build binary: %v", err)
	}

	// Generate a UUID for password
	uuidCmd := exec.Command("uuidgen")
	var uuidOut bytes.Buffer
	uuidCmd.Stdout = &uuidOut
	if err := uuidCmd.Run(); err != nil {
		log.Fatalf("Failed to generate UUID: %v", err)
	}
	password := strings.TrimSpace(uuidOut.String())

	// Create systemd service file with the password
	serviceContent := strings.ReplaceAll(systemdService, "{{USER}}", username)
	serviceContent = strings.ReplaceAll(serviceContent, "{{PASSWORD}}", password)

	tmpServiceFile := "/tmp/mobileshell.service"
	if err := os.WriteFile(tmpServiceFile, []byte(serviceContent), 0o644); err != nil {
		log.Fatalf("Failed to write service file: %v", err)
	}

	// Copy binary to remote server
	fmt.Println("Copying binary to remote server...")
	remotePath := fmt.Sprintf("root@%s:/home/%s/mobileshell", hostname, username)
	scpCmd := exec.Command("scp", "mobileshell", remotePath)
	scpCmd.Stdout = os.Stdout
	scpCmd.Stderr = os.Stderr
	if err := scpCmd.Run(); err != nil {
		log.Fatalf("Failed to copy binary: %v", err)
	}

	// Copy service file to remote server
	fmt.Println("Copying service file to remote server...")
	scpServiceCmd := exec.Command("scp", tmpServiceFile, fmt.Sprintf("root@%s:/tmp/mobileshell.service", hostname))
	scpServiceCmd.Stdout = os.Stdout
	scpServiceCmd.Stderr = os.Stderr
	if err := scpServiceCmd.Run(); err != nil {
		log.Fatalf("Failed to copy service file: %v", err)
	}

	// Install and start the service
	fmt.Println("Installing and starting systemd service...")
	installScript := fmt.Sprintf(`
		chown %s:%s /home/%s/mobileshell
		chmod +x /home/%s/mobileshell
		mv /tmp/mobileshell.service /etc/systemd/system/mobileshell.service
		systemctl daemon-reload
		systemctl enable mobileshell
		systemctl restart mobileshell
		systemctl status mobileshell
	`, username, username, username, username)

	sshCmd := exec.Command("ssh", fmt.Sprintf("root@%s", hostname), installScript)
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr
	if err := sshCmd.Run(); err != nil {
		log.Fatalf("Failed to install service: %v", err)
	}

	// Clean up local files
	_ = os.Remove("mobileshell")
	_ = os.Remove(tmpServiceFile)

	fmt.Println("\n=== Installation Complete ===")
	fmt.Printf("MobileShell is now running on %s\n", hostname)
	fmt.Printf("Access it at: http://%s:22123/\n", hostname)
	fmt.Printf("Login password (UUID): %s\n", password)
	fmt.Println("\nMake sure to configure TLS termination (e.g., nginx) for production use.")
	fmt.Println("Save the password securely - you'll need it to login.")
}
