package pbnats

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pocketbase/pocketbase"
	"github.com/spf13/cobra"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// RegisterCommands adds NATS-related CLI commands to the PocketBase application.
// This enables users to export NATS configuration files for server setup.
//
// USAGE:
//   ./myapp nats export --output ./nats-config/
//   ./myapp nats export --operator-jwt
//   ./myapp nats export --config
//
// PARAMETERS:
//   - app: PocketBase application instance
//
// COMMANDS ADDED:
//   - nats: Parent command for NATS operations
//   - nats export: Export NATS server configuration files
func RegisterCommands(app *pocketbase.PocketBase) {
	natsCmd := &cobra.Command{
		Use:   "nats",
		Short: "NATS server configuration commands",
		Long:  "Commands for managing NATS server configuration and JWT exports.",
	}

	exportCmd := createExportCommand(app)
	natsCmd.AddCommand(exportCmd)

	app.RootCmd.AddCommand(natsCmd)
}

// createExportCommand creates the 'nats export' subcommand.
func createExportCommand(app *pocketbase.PocketBase) *cobra.Command {
	var outputDir string
	var operatorJWTOnly bool
	var configOnly bool
	var operatorConfOnly bool
	var serverName string
	var natsPort int
	var jetstreamStoreDir string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export NATS server configuration files",
		Long: `Export NATS server configuration files for deploying a NATS server.

This command exports:
  - operator.jwt: The operator JWT for NATS server authentication
  - operator.conf: Operator configuration with system account
  - nats.conf: Example NATS server configuration

The exported files can be used to bootstrap a NATS server that works
with the pb-nats JWT authentication system.

Examples:
  # Export all files to a directory
  ./myapp nats export --output ./nats-config/

  # Export only the operator JWT to stdout
  ./myapp nats export --operator-jwt

  # Export only the nats.conf to stdout
  ./myapp nats export --config

  # Export only the operator.conf to stdout
  ./myapp nats export --operator-conf

  # Customize server settings
  ./myapp nats export --output ./nats-config/ --server-name my-nats --port 4222`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bootstrap the app to access the database
			if err := app.Bootstrap(); err != nil {
				return fmt.Errorf("failed to bootstrap app: %w", err)
			}

			// Get operator and system account data
			operator, sysAccount, err := getOperatorAndSystemAccount(app)
			if err != nil {
				return err
			}

			// Handle single-output modes
			if operatorJWTOnly {
				fmt.Println(operator.JWT)
				return nil
			}

			if operatorConfOnly {
				conf := generateOperatorConf(operator, sysAccount)
				fmt.Println(conf)
				return nil
			}

			if configOnly {
				conf := generateNATSConf(serverName, natsPort, jetstreamStoreDir)
				fmt.Println(conf)
				return nil
			}

			// Export all files to directory
			if outputDir == "" {
				return fmt.Errorf("--output directory is required when not using --operator-jwt, --config, or --operator-conf")
			}

			return exportAllFiles(outputDir, operator, sysAccount, serverName, natsPort, jetstreamStoreDir)
		},
	}

	// Define flags
	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "Output directory for configuration files")
	cmd.Flags().BoolVar(&operatorJWTOnly, "operator-jwt", false, "Output only the operator JWT to stdout")
	cmd.Flags().BoolVar(&configOnly, "config", false, "Output only the nats.conf to stdout")
	cmd.Flags().BoolVar(&operatorConfOnly, "operator-conf", false, "Output only the operator.conf to stdout")
	cmd.Flags().StringVar(&serverName, "server-name", "nats-server", "NATS server name")
	cmd.Flags().IntVar(&natsPort, "port", 4222, "NATS server port")
	cmd.Flags().StringVar(&jetstreamStoreDir, "jetstream-store", "./storage/jetstream", "JetStream storage directory")

	return cmd
}

// getOperatorAndSystemAccount retrieves the operator and system account from the database.
func getOperatorAndSystemAccount(app *pocketbase.PocketBase) (*pbtypes.SystemOperatorRecord, *systemAccountInfo, error) {
	// Get operator
	operatorRecords, err := app.FindAllRecords(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find operator records: %w", err)
	}
	if len(operatorRecords) == 0 {
		return nil, nil, fmt.Errorf("no operator found - please run the server first to initialize the system")
	}

	record := operatorRecords[0]
	operator := &pbtypes.SystemOperatorRecord{
		ID:               record.Id,
		Name:             record.GetString("name"),
		PublicKey:        record.GetString("public_key"),
		SigningPublicKey: record.GetString("signing_public_key"),
		JWT:              record.GetString("jwt"),
	}

	if operator.JWT == "" {
		return nil, nil, fmt.Errorf("operator JWT is empty - please run the server first to complete initialization")
	}

	// Get system account
	sysAccountRecords, err := app.FindAllRecords(DefaultAccountCollectionName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to find account records: %w", err)
	}

	var sysAccount *systemAccountInfo
	for _, rec := range sysAccountRecords {
		if rec.GetString("name") == "System Account" {
			sysAccount = &systemAccountInfo{
				PublicKey: rec.GetString("public_key"),
				JWT:       rec.GetString("jwt"),
			}
			break
		}
	}

	if sysAccount == nil {
		return nil, nil, fmt.Errorf("system account not found - please run the server first to initialize the system")
	}

	if sysAccount.JWT == "" {
		return nil, nil, fmt.Errorf("system account JWT is empty - please run the server first to complete initialization")
	}

	return operator, sysAccount, nil
}

// systemAccountInfo holds the minimal system account data needed for config generation.
type systemAccountInfo struct {
	PublicKey string
	JWT       string
}

// generateOperatorConf generates the operator.conf content.
func generateOperatorConf(operator *pbtypes.SystemOperatorRecord, sysAccount *systemAccountInfo) string {
	var sb strings.Builder

	sb.WriteString("# Operator Configuration\n")
	sb.WriteString("# Generated by pb-nats\n")
	sb.WriteString("#\n")
	sb.WriteString("# This file contains the operator JWT reference and system account configuration.\n")
	sb.WriteString("# Include this file in your nats.conf using: include operator.conf\n\n")

	sb.WriteString("# Operator JWT file path\n")
	sb.WriteString("operator: './operator.jwt'\n\n")

	sb.WriteString("# System account public key\n")
	sb.WriteString(fmt.Sprintf("system_account: '%s'\n\n", sysAccount.PublicKey))

	sb.WriteString("# Resolver preload - System account JWT\n")
	sb.WriteString("# This preloads the system account so NATS can start without needing to fetch it\n")
	sb.WriteString("resolver_preload: {\n")
	sb.WriteString(fmt.Sprintf("  \"%s\": \"%s\"\n", sysAccount.PublicKey, sysAccount.JWT))
	sb.WriteString("}\n")

	return sb.String()
}

// generateNATSConf generates the nats.conf content.
func generateNATSConf(serverName string, port int, jetstreamStoreDir string) string {
	var sb strings.Builder

	sb.WriteString("# NATS Server Configuration\n")
	sb.WriteString("# Generated by pb-nats\n")
	sb.WriteString("#\n")
	sb.WriteString("# This is an example configuration for use with pb-nats JWT authentication.\n")
	sb.WriteString("# Customize as needed for your deployment.\n\n")

	sb.WriteString("# Server identification\n")
	sb.WriteString(fmt.Sprintf("server_name: %s\n", serverName))
	sb.WriteString(fmt.Sprintf("port: %d\n\n", port))

	sb.WriteString("# Include operator configuration\n")
	sb.WriteString("include operator.conf\n\n")

	sb.WriteString("# JWT Resolver Configuration\n")
	sb.WriteString("# The resolver stores account JWTs published by pb-nats\n")
	sb.WriteString("resolver: {\n")
	sb.WriteString("  type: full\n")
	sb.WriteString("  # Directory where account JWTs will be stored\n")
	sb.WriteString("  dir: './jwt'\n")
	sb.WriteString("  # Allow JWT deletion (enables account removal)\n")
	sb.WriteString("  allow_delete: true\n")
	sb.WriteString("  # Interval for cluster JWT synchronization\n")
	sb.WriteString("  interval: \"2m\"\n")
	sb.WriteString("  # Maximum number of JWTs to store\n")
	sb.WriteString("  limit: 1000\n")
	sb.WriteString("}\n\n")

	sb.WriteString("# JetStream Configuration\n")
	sb.WriteString("jetstream: {\n")
	sb.WriteString(fmt.Sprintf("  store_dir: '%s'\n", jetstreamStoreDir))
	sb.WriteString("}\n\n")

	sb.WriteString("# Optional: HTTP monitoring port\n")
	sb.WriteString("# http_port: 8222\n\n")

	sb.WriteString("# Optional: MQTT support\n")
	sb.WriteString("# mqtt {\n")
	sb.WriteString("#   port: 1883\n")
	sb.WriteString("# }\n\n")

	sb.WriteString("# Optional: WebSocket support\n")
	sb.WriteString("# websocket {\n")
	sb.WriteString("#   port: 8080\n")
	sb.WriteString("#   # For TLS:\n")
	sb.WriteString("#   # tls {\n")
	sb.WriteString("#   #   cert_file: \"/path/to/cert.pem\"\n")
	sb.WriteString("#   #   key_file: \"/path/to/key.pem\"\n")
	sb.WriteString("#   # }\n")
	sb.WriteString("# }\n")

	return sb.String()
}

// generateReadme generates the README.txt content with setup instructions.
func generateReadme(operator *pbtypes.SystemOperatorRecord) string {
	var sb strings.Builder

	sb.WriteString("NATS Server Configuration - Generated by pb-nats\n")
	sb.WriteString("================================================\n\n")

	sb.WriteString("This directory contains the configuration files needed to run a NATS server\n")
	sb.WriteString("that integrates with your PocketBase application using JWT authentication.\n\n")

	sb.WriteString("FILES\n")
	sb.WriteString("-----\n")
	sb.WriteString("  operator.jwt  - The operator JWT (root of trust)\n")
	sb.WriteString("  operator.conf - Operator and system account configuration\n")
	sb.WriteString("  nats.conf     - Main NATS server configuration\n\n")

	sb.WriteString("QUICK START\n")
	sb.WriteString("-----------\n")
	sb.WriteString("1. Create the JWT storage directory:\n")
	sb.WriteString("   mkdir -p ./jwt\n\n")

	sb.WriteString("2. Create the JetStream storage directory:\n")
	sb.WriteString("   mkdir -p ./storage/jetstream\n\n")

	sb.WriteString("3. Start the NATS server:\n")
	sb.WriteString("   nats-server -c nats.conf\n\n")

	sb.WriteString("4. Start your PocketBase application - it will automatically\n")
	sb.WriteString("   connect to NATS and begin synchronizing account JWTs.\n\n")

	sb.WriteString("CONFIGURATION\n")
	sb.WriteString("-------------\n")
	sb.WriteString(fmt.Sprintf("Operator Name: %s\n", operator.Name))
	sb.WriteString(fmt.Sprintf("Operator Public Key: %s\n\n", operator.PublicKey))

	sb.WriteString("CUSTOMIZATION\n")
	sb.WriteString("-------------\n")
	sb.WriteString("Edit nats.conf to:\n")
	sb.WriteString("  - Change the server port (default: 4222)\n")
	sb.WriteString("  - Enable MQTT support\n")
	sb.WriteString("  - Enable WebSocket support with TLS\n")
	sb.WriteString("  - Add HTTP monitoring\n")
	sb.WriteString("  - Configure clustering\n\n")

	sb.WriteString("For more information, see:\n")
	sb.WriteString("  - NATS documentation: https://docs.nats.io/\n")
	sb.WriteString("  - pb-nats documentation: https://github.com/skeeeon/pb-nats\n")

	return sb.String()
}

// exportAllFiles writes all configuration files to the output directory.
func exportAllFiles(outputDir string, operator *pbtypes.SystemOperatorRecord, sysAccount *systemAccountInfo, serverName string, port int, jetstreamStoreDir string) error {
	// Create output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Write operator.jwt
	operatorJWTPath := filepath.Join(outputDir, "operator.jwt")
	if err := os.WriteFile(operatorJWTPath, []byte(operator.JWT), 0644); err != nil {
		return fmt.Errorf("failed to write operator.jwt: %w", err)
	}
	fmt.Printf("✅ Created: %s\n", operatorJWTPath)

	// Write operator.conf
	operatorConfPath := filepath.Join(outputDir, "operator.conf")
	operatorConf := generateOperatorConf(operator, sysAccount)
	if err := os.WriteFile(operatorConfPath, []byte(operatorConf), 0644); err != nil {
		return fmt.Errorf("failed to write operator.conf: %w", err)
	}
	fmt.Printf("✅ Created: %s\n", operatorConfPath)

	// Write nats.conf
	natsConfPath := filepath.Join(outputDir, "nats.conf")
	natsConf := generateNATSConf(serverName, port, jetstreamStoreDir)
	if err := os.WriteFile(natsConfPath, []byte(natsConf), 0644); err != nil {
		return fmt.Errorf("failed to write nats.conf: %w", err)
	}
	fmt.Printf("✅ Created: %s\n", natsConfPath)

	// Write README.txt
	readmePath := filepath.Join(outputDir, "README.txt")
	readme := generateReadme(operator)
	if err := os.WriteFile(readmePath, []byte(readme), 0644); err != nil {
		return fmt.Errorf("failed to write README.txt: %w", err)
	}
	fmt.Printf("✅ Created: %s\n", readmePath)

	fmt.Println()
	fmt.Println("🚀 NATS configuration exported successfully!")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. cd %s\n", outputDir)
	fmt.Println("  2. mkdir -p ./jwt ./storage/jetstream")
	fmt.Println("  3. nats-server -c nats.conf")
	fmt.Println("  4. Start your PocketBase application")

	return nil
}

// RegisterCommandsWithOptions adds NATS CLI commands with custom options.
// This allows customization of default values for the export command.
//
// PARAMETERS:
//   - app: PocketBase application instance
//   - opts: Command options for customization
func RegisterCommandsWithOptions(app *pocketbase.PocketBase, opts CommandOptions) {
	natsCmd := &cobra.Command{
		Use:   "nats",
		Short: "NATS server configuration commands",
		Long:  "Commands for managing NATS server configuration and JWT exports.",
	}

	exportCmd := createExportCommandWithOptions(app, opts)
	natsCmd.AddCommand(exportCmd)

	app.RootCmd.AddCommand(natsCmd)
}

// CommandOptions allows customization of command defaults.
type CommandOptions struct {
	DefaultServerName      string
	DefaultPort            int
	DefaultJetstreamStore  string
	DefaultOutputDir       string
}

// DefaultCommandOptions returns sensible defaults for command options.
func DefaultCommandOptions() CommandOptions {
	return CommandOptions{
		DefaultServerName:     "nats-server",
		DefaultPort:           4222,
		DefaultJetstreamStore: "./storage/jetstream",
		DefaultOutputDir:      "",
	}
}

// createExportCommandWithOptions creates the export command with custom defaults.
func createExportCommandWithOptions(app *pocketbase.PocketBase, opts CommandOptions) *cobra.Command {
	var outputDir string
	var operatorJWTOnly bool
	var configOnly bool
	var operatorConfOnly bool
	var serverName string
	var natsPort int
	var jetstreamStoreDir string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export NATS server configuration files",
		Long: `Export NATS server configuration files for deploying a NATS server.

This command exports:
  - operator.jwt: The operator JWT for NATS server authentication
  - operator.conf: Operator configuration with system account
  - nats.conf: Example NATS server configuration

Examples:
  ./myapp nats export --output ./nats-config/
  ./myapp nats export --operator-jwt
  ./myapp nats export --config
  ./myapp nats export --operator-conf`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := app.Bootstrap(); err != nil {
				return fmt.Errorf("failed to bootstrap app: %w", err)
			}

			operator, sysAccount, err := getOperatorAndSystemAccount(app)
			if err != nil {
				return err
			}

			if operatorJWTOnly {
				fmt.Println(operator.JWT)
				return nil
			}

			if operatorConfOnly {
				conf := generateOperatorConf(operator, sysAccount)
				fmt.Println(conf)
				return nil
			}

			if configOnly {
				conf := generateNATSConf(serverName, natsPort, jetstreamStoreDir)
				fmt.Println(conf)
				return nil
			}

			if outputDir == "" {
				return fmt.Errorf("--output directory is required when not using --operator-jwt, --config, or --operator-conf")
			}

			return exportAllFiles(outputDir, operator, sysAccount, serverName, natsPort, jetstreamStoreDir)
		},
	}

	cmd.Flags().StringVarP(&outputDir, "output", "o", opts.DefaultOutputDir, "Output directory for configuration files")
	cmd.Flags().BoolVar(&operatorJWTOnly, "operator-jwt", false, "Output only the operator JWT to stdout")
	cmd.Flags().BoolVar(&configOnly, "config", false, "Output only the nats.conf to stdout")
	cmd.Flags().BoolVar(&operatorConfOnly, "operator-conf", false, "Output only the operator.conf to stdout")
	cmd.Flags().StringVar(&serverName, "server-name", opts.DefaultServerName, "NATS server name")
	cmd.Flags().IntVar(&natsPort, "port", opts.DefaultPort, "NATS server port")
	cmd.Flags().StringVar(&jetstreamStoreDir, "jetstream-store", opts.DefaultJetstreamStore, "JetStream storage directory")

	return cmd
}
